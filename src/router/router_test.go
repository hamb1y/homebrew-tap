package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, providers map[string]ProviderConfig) *Router {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(Config{Providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "router.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := NewRouter(filepath.Join(dir, "router.json"), filepath.Join(dir, "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	r.httpClient.Transport = &http.Transport{DialContext: wireDial}
	return r
}
func provider(endpoint, name string) ProviderConfig {
	return ProviderConfig{Type: "openai_compatible", Enabled: true, BaseURL: endpoint, Models: map[string]ModelConfig{name: {Enabled: true, UpstreamModel: "upstream-" + name, Class: "smart"}}}
}
func post(t *testing.T, r *Router, payload string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.chatCompletions(w, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(payload)))
	return w
}

func TestPreserveRequestFields(t *testing.T) {
	upstream := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for _, field := range []string{"tools", "tool_choice", "response_format", "seed", "metadata", "messages"} {
			if _, ok := body[field]; !ok {
				t.Errorf("lost %s", field)
			}
		}
		if string(body["model"]) != `"upstream-local"` {
			t.Errorf("model %s", body["model"])
		}
		if !strings.Contains(string(body["messages"]), "tool_calls") {
			t.Error("lost message tool_calls")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unauthenticated local provider should omit Authorization")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	r := fixture(t, map[string]ProviderConfig{"local": provider(upstream.URL+"/v1", "local")})
	w := post(t, r, `{"model":"local","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call"}]}],"tools":[],"tool_choice":"auto","response_format":{"type":"json_object"},"seed":7,"metadata":{"tag":"value"}}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Error("content type changed")
	}
}

func TestFailoverAndPinnedModel(t *testing.T) {
	var failed, success atomic.Int32
	bad := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { failed.Add(1); w.WriteHeader(429) }))
	defer bad.Close()
	good := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { success.Add(1); io.WriteString(w, `{"ok":true}`) }))
	defer good.Close()
	r := fixture(t, map[string]ProviderConfig{"bad": provider(bad.URL, "a"), "good": provider(good.URL, "z")})
	if got := r.getActive("smart"); got != "a" {
		t.Fatalf("initial model %q", got)
	}
	w := post(t, r, `{"model":"a","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 429 || success.Load() != 0 {
		t.Fatalf("pinned request failed over: %d", w.Code)
	}
	w = post(t, r, `{"model":"smart","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 || success.Load() != 1 || failed.Load() != 2 || r.getActive("smart") != "z" {
		t.Fatalf("failover: %d %s", w.Code, w.Body.String())
	}
}

func TestSSEArrivesBeforeUpstreamFinishes(t *testing.T) {
	release := make(chan struct{})
	upstream := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Connection", "X-Private, keep-alive")
		w.Header().Set("X-Private", "remove-me")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	r := fixture(t, map[string]ProviderConfig{"local": provider(upstream.URL, "local")})
	proxy := wireServer(t, http.HandlerFunc(r.chatCompletions))
	defer proxy.Close()
	client := http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: wireDial}}
	resp, err := client.Post(proxy.URL, "application/json", strings.NewReader(`{"model":"local","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Private") != "" || resp.Header.Get("Connection") != "" {
		t.Error("hop headers leaked")
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	close(release)
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event %q %v", line, err)
	}
}

func TestValidationAndPrivateInit(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config", "router.json")
	keys := filepath.Join(dir, "config", "keys.json")
	if err := initConfig(config, keys); err != nil {
		t.Fatal(err)
	}
	r, err := NewRouter(config, keys)
	if err != nil {
		t.Fatal(err)
	}
	if r.config.Listen != "127.0.0.1:8080" {
		t.Error(r.config.Listen)
	}
	before, _ := os.ReadFile(config)
	if err := initConfig(config, keys); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(config)
	if string(before) != string(after) {
		t.Error("init overwrote config")
	}
	for _, path := range []string{config, keys} {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Errorf("mode %s", info.Mode())
		}
	}
	p1 := provider("http://localhost", "a")
	p2 := provider("http://localhost", "a")
	if err := validateConfig(Config{Providers: map[string]ProviderConfig{"one": p1, "two": p2}}); err == nil {
		t.Error("duplicate accepted")
	}
	p1.BaseURL = "file:///etc/passwd"
	if err := validateConfig(Config{Providers: map[string]ProviderConfig{"one": p1}}); err == nil {
		t.Error("URL accepted")
	}
	if err := os.WriteFile(config, append(before, []byte("{}")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRouter(config, keys); err == nil {
		t.Error("trailing config JSON accepted")
	}
}

func TestBadRequestsAndMethods(t *testing.T) {
	r := fixture(t, map[string]ProviderConfig{"local": provider("http://127.0.0.1:1", "local")})
	for _, payload := range []string{"null", "[]", `{"messages":[]}`, `{"messages":[]} {}`, `{"messages":[1]}`} {
		if w := post(t, r, payload); w.Code != 400 {
			t.Errorf("%s => %d", payload, w.Code)
		}
	}
	for _, handler := range []http.HandlerFunc{r.healthz, r.status, r.model, r.models} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("POST", "/", nil))
		if w.Code != 405 {
			t.Error(w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.setModel(w, httptest.NewRequest("POST", "/set-model", strings.NewReader(`{"model":"local"} {}`)))
	if w.Code != 400 {
		t.Error(w.Code)
	}
}

func TestAnthropicRejectsUnsupportedInsteadOfDropping(t *testing.T) {
	if err := validateAnthropicFields(map[string]json.RawMessage{"tools": json.RawMessage(`[]`)}, ChatRequest{}); err == nil {
		t.Error("tools silently dropped")
	}
	if err := validateAnthropicFields(nil, ChatRequest{Messages: []ChatMessage{{Role: "tool", Content: "result"}}}); err == nil {
		t.Error("tool role silently changed")
	}
}

func TestOAuthAccessTokenOnly(t *testing.T) {
	r := fixture(t, map[string]ProviderConfig{"local": provider("http://localhost", "local")})
	path := filepath.Join(t.TempDir(), "oauth.json")
	os.WriteFile(path, []byte(`{"id_token":"identity-only"}`), 0600)
	if _, err := r.oauthBearer(ProviderConfig{AuthFile: path}); err == nil {
		t.Error("identity token accepted as access token")
	}
	os.WriteFile(path, []byte(`{"tokens":{"access_token":"access"}}`), 0600)
	if token, err := r.oauthBearer(ProviderConfig{AuthFile: path}); err != nil || token != "access" {
		t.Errorf("%q %v", token, err)
	}
}

func TestRedirectDoesNotLeakAuthorization(t *testing.T) {
	var visited atomic.Bool
	target := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visited.Store(true) }))
	defer target.Close()
	upstream := wireServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer upstream.Close()
	r := fixture(t, map[string]ProviderConfig{"local": provider(upstream.URL, "local")})
	if w := post(t, r, `{"model":"local","messages":[{"role":"user","content":"hi"}]}`); w.Code != 307 {
		t.Error(w.Code)
	}
	if visited.Load() {
		t.Error("redirect followed")
	}
}

// Exercise the real HTTP client/server and flushing over net.Pipe when the
// execution environment disallows socket bind. Set ROUTER_TEST_NETWORK=1 to
// repeat the same tests over operating-system TCP sockets.
var wireRegistry sync.Map
var wireID atomic.Int32

type wireAddr string

func (a wireAddr) Network() string { return "pipe" }
func (a wireAddr) String() string  { return string(a) }

type wireListener struct {
	conns chan net.Conn
	done  chan struct{}
	addr  wireAddr
	once  sync.Once
}

func (l *wireListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *wireListener) Close() error {
	l.once.Do(func() { close(l.done); wireRegistry.Delete(string(l.addr)) })
	return nil
}
func (l *wireListener) Addr() net.Addr { return l.addr }
func wireServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	if os.Getenv("ROUTER_TEST_NETWORK") == "1" {
		return httptest.NewServer(handler)
	}
	addr := wireAddr(fmt.Sprintf("127.0.0.1:%d", 20000+wireID.Add(1)))
	listener := &wireListener{conns: make(chan net.Conn), done: make(chan struct{}), addr: addr}
	wireRegistry.Store(string(addr), listener)
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	return server
}
func wireDial(ctx context.Context, network, addr string) (net.Conn, error) {
	if os.Getenv("ROUTER_TEST_NETWORK") == "1" {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	raw, ok := wireRegistry.Load(addr)
	if !ok {
		return nil, fmt.Errorf("unknown test server %s", addr)
	}
	listener := raw.(*wireListener)
	client, server := net.Pipe()
	select {
	case listener.conns <- server:
		return client, nil
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	case <-listener.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}
