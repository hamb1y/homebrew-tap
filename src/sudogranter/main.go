package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

const (
	version     = "0.1.0"
	defaultAddr = "127.0.0.1:64420"
	configDir   = "/etc/sudogranter"
	configPath  = configDir + "/config.json"
	serviceLog  = configDir + "/sudogranter.log"
	runLogDir   = configDir + "/runs"
	maxBodySize = 1 << 20
)

type config struct {
	BearerToken string `json:"bearer_token"`
	Addr        string `json:"addr,omitempty"`
}

type server struct {
	cfg    config
	logger *log.Logger
	jobs   sync.Map
}

type commandRequest struct {
	Interpreter string `json:"interpreter"`
	Command     string `json:"command"`
}

type commandResponse struct {
	ID        string `json:"id"`
	StreamURL string `json:"stream_url"`
}

type job struct {
	id      string
	path    string
	done    chan struct{}
	exit    atomic.Int32
	errText atomic.Value
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("sudogranter %s\n", version)
		return
	}

	if err := os.MkdirAll(runLogDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "create %s: %v\n", runLogDir, err)
		os.Exit(1)
	}

	logger, closeLog, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "open service log: %v\n", err)
		os.Exit(1)
	}
	defer closeLog()

	cfg, err := loadConfig()
	if err != nil {
		logger.Error("configuration error", "err", err)
		os.Exit(1)
	}
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}

	s := &server{cfg: cfg, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)

	logger.Info("starting sudogranter", "addr", cfg.Addr, "config", configPath, "runs", runLogDir)
	if err := http.ListenAndServe(cfg.Addr, s.logRequests(mux)); err != nil {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func newLogger() (*log.Logger, func(), error) {
	f, err := os.OpenFile(serviceLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, err
	}
	logger := log.NewWithOptions(io.MultiWriter(os.Stderr, f), log.Options{
		ReportTimestamp: true,
		TimeFormat:      time.RFC3339,
	})
	return logger, func() { _ = f.Close() }, nil
}

func loadConfig() (config, error) {
	var cfg config
	if token := strings.TrimSpace(os.Getenv("SUDOGRANTER_TOKEN")); token != "" {
		cfg.BearerToken = token
	}
	if addr := strings.TrimSpace(os.Getenv("SUDOGRANTER_ADDR")); addr != "" {
		cfg.Addr = addr
	}

	b, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && cfg.BearerToken != "" {
			return cfg, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			sample := []byte("{\n  \"bearer_token\": \"replace-with-a-long-random-token\",\n  \"addr\": \"127.0.0.1:64420\"\n}\n")
			_ = os.WriteFile(configPath+".example", sample, 0600)
			return cfg, fmt.Errorf("missing bearer token; set SUDOGRANTER_TOKEN or create %s", configPath)
		}
		return cfg, err
	}

	var fileCfg config
	if err := json.Unmarshal(b, &fileCfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", configPath, err)
	}
	if cfg.BearerToken == "" {
		cfg.BearerToken = strings.TrimSpace(fileCfg.BearerToken)
	}
	if cfg.Addr == "" {
		cfg.Addr = strings.TrimSpace(fileCfg.Addr)
	}
	if cfg.BearerToken == "" {
		return cfg, fmt.Errorf("missing bearer_token in %s", configPath)
	}
	return cfg, nil
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", clientIP(r),
			"status", sw.status,
			"bytes", sw.bytes,
			"duration", time.Since(start),
		)
	})
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *server) handle(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized\n", http.StatusUnauthorized)
		return
	}

	if r.Method == http.MethodPost && (r.URL.Path == "/" || r.URL.Path == "/run") {
		s.submit(w, r)
		return
	}
	if r.Method == http.MethodGet {
		id, ok := streamID(r.URL.Path)
		if ok {
			s.stream(w, r, id)
			return
		}
	}

	http.NotFound(w, r)
}

func (s *server) authorized(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == s.cfg.BearerToken
}

func (s *server) submit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	req, err := parseCommand(r.Body)
	if err != nil {
		http.Error(w, err.Error()+"\n", http.StatusBadRequest)
		return
	}

	id, err := uuidV7()
	if err != nil {
		http.Error(w, "generate uuid\n", http.StatusInternalServerError)
		return
	}
	j := &job{
		id:   id,
		path: filepath.Join(runLogDir, id+".log"),
		done: make(chan struct{}),
	}
	if err := createRunLog(j.path); err != nil {
		http.Error(w, "create run log\n", http.StatusInternalServerError)
		return
	}
	s.jobs.Store(id, j)

	go s.runCommand(j, req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(commandResponse{
		ID:        id,
		StreamURL: "/" + id + "/",
	})
}

func createRunLog(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

func parseCommand(r io.Reader) (commandRequest, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBodySize+1))
	if err != nil {
		return commandRequest{}, err
	}
	if len(b) > maxBodySize {
		return commandRequest{}, fmt.Errorf("request body too large")
	}
	body := strings.TrimSpace(string(b))
	if body == "" {
		return commandRequest{}, fmt.Errorf("empty command")
	}

	var req commandRequest
	if strings.HasPrefix(body, "{") {
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			return commandRequest{}, fmt.Errorf("invalid json: %w", err)
		}
	} else if left, right, ok := strings.Cut(body, " <- "); ok {
		req.Interpreter = strings.TrimSpace(left)
		req.Command = strings.TrimSpace(right)
	} else if left, right, ok := strings.Cut(body, ","); ok && strings.HasPrefix(strings.TrimSpace(left), "/") {
		req.Interpreter = strings.TrimSpace(left)
		req.Command = strings.TrimSpace(right)
	} else {
		req.Interpreter = "/bin/bash"
		req.Command = body
	}

	req.Interpreter = strings.TrimSpace(req.Interpreter)
	req.Command = strings.TrimSpace(req.Command)
	if req.Interpreter == "" {
		req.Interpreter = "/bin/bash"
	}
	if !strings.HasPrefix(req.Interpreter, "/") || strings.ContainsRune(req.Interpreter, 0) {
		return commandRequest{}, fmt.Errorf("interpreter must be an absolute path")
	}
	if req.Command == "" || strings.ContainsRune(req.Command, 0) {
		return commandRequest{}, fmt.Errorf("command must be non-empty")
	}
	return req, nil
}

func (s *server) runCommand(j *job, req commandRequest) {
	defer s.jobs.Delete(j.id)
	defer close(j.done)

	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		j.exit.Store(-1)
		j.errText.Store(err.Error())
		s.logger.Error("open run log", "id", j.id, "err", err)
		return
	}
	defer f.Close()

	lw := &lockedWriter{w: f}
	startLine := fmt.Sprintf("sudogranter id=%s started=%s interpreter=%s\n", j.id, time.Now().Format(time.RFC3339), req.Interpreter)
	_, _ = lw.Write([]byte(startLine))

	args := append([]string{"--", req.Interpreter}, interpreterArgs(req.Interpreter, req.Command)...)
	cmd := exec.CommandContext(context.Background(), "sudo", args...)
	cmd.Stdout = lw
	cmd.Stderr = lw

	s.logger.Info("command started", "id", j.id, "interpreter", req.Interpreter)
	err = cmd.Run()
	exitCode := int32(0)
	if err != nil {
		exitCode = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = int32(ee.ExitCode())
		}
		j.errText.Store(err.Error())
	}
	j.exit.Store(exitCode)
	endLine := fmt.Sprintf("\nsudogranter id=%s finished=%s exit=%d\n", j.id, time.Now().Format(time.RFC3339), exitCode)
	_, _ = lw.Write([]byte(endLine))
	s.logger.Info("command finished", "id", j.id, "exit", exitCode, "err", valueString(j.errText.Load()))
}

func interpreterArgs(interpreter, command string) []string {
	switch filepath.Base(interpreter) {
	case "bash", "sh", "zsh", "ksh":
		return []string{"-lc", command}
	default:
		return []string{"-c", command}
	}
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(b)
}

func (s *server) stream(w http.ResponseWriter, r *http.Request, id string) {
	path := filepath.Join(runLogDir, id+".log")
	if filepath.Base(path) != id+".log" {
		http.Error(w, "invalid id\n", http.StatusBadRequest)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "open stream\n", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	buf := make([]byte, 32*1024)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return
		}

		if done := s.jobDone(id); done {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (s *server) jobDone(id string) bool {
	v, ok := s.jobs.Load(id)
	if !ok {
		return true
	}
	j := v.(*job)
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

func streamID(path string) (string, bool) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return "", false
	}
	if len(trimmed) != 36 {
		return "", false
	}
	for i, r := range trimmed {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return "", false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", false
		}
	}
	return trimmed, true
}

func uuidV7() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80

	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst), nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func valueString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
