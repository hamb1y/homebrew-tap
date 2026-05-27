package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen    string                    `json:"listen"`
	TimeoutMS int                       `json:"timeout_ms"`
	Providers map[string]ProviderConfig `json:"providers"`
}

type ProviderConfig struct {
	Type      string                 `json:"type"`
	Enabled   bool                   `json:"enabled"`
	BaseURL   string                 `json:"base_url"`
	APIKeyRef string                 `json:"api_key_ref"`
	AuthFile  string                 `json:"auth_file"`
	ProjectID string                 `json:"project_id"`
	Location  string                 `json:"location"`
	Headers   map[string]string      `json:"headers"`
	Models    map[string]ModelConfig `json:"models"`
}

type ModelConfig struct {
	Enabled       bool   `json:"enabled"`
	UpstreamModel string `json:"upstream_model"`
	Class         string `json:"class"`
}

type KeysFile struct {
	Keys map[string]string `json:"keys"`
}

type Router struct {
	config     Config
	keys       map[string]string
	configDir  string
	httpClient *http.Client
	mu         sync.RWMutex
	active     map[string]string
	current    string
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Stream      bool          `json:"stream,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
}

type ChatMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type ModelChoice struct {
	PublicModel   string
	UpstreamModel string
	Class         string
	ProviderName  string
	Provider      ProviderConfig
}

type StatusResponse struct {
	Status              string            `json:"status"`
	Listen              string            `json:"listen"`
	CurrentIntelligence string            `json:"current_intelligence"`
	CurrentModel        string            `json:"current_model"`
	CurrentProvider     string            `json:"current_provider,omitempty"`
	Active              map[string]string `json:"active"`
	Providers           map[string]string `json:"providers"`
	Models              []StatusModel     `json:"models"`
}

type StatusModel struct {
	Name     string `json:"name"`
	Class    string `json:"class"`
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
	Active   bool   `json:"active"`
}

type SetModelRequest struct {
	Model string `json:"model"`
}

type SetIntelligenceRequest struct {
	Intelligence string `json:"intelligence"`
	Class        string `json:"class"`
}

type OpenAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

type OpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type AnthropicResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"`
	Role         string             `json:"role"`
	Model        string             `json:"model"`
	Content      []AnthropicContent `json:"content"`
	StopReason   string             `json:"stop_reason"`
	StopSequence string             `json:"stop_sequence"`
	Usage        AnthropicUsage     `json:"usage"`
}

type AnthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func main() {
	configPath := flag.String("config", defaultConfigPath("router.json"), "router config file")
	keysPath := flag.String("keys", defaultConfigPath("keys.json"), "api key config file")
	flag.Parse()

	router, err := NewRouter(*configPath, *keysPath)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", router.healthz)
	mux.HandleFunc("/status", router.status)
	mux.HandleFunc("/v1/status", router.status)
	mux.HandleFunc("/model", router.model)
	mux.HandleFunc("/set-model", router.setModel)
	mux.HandleFunc("/set-intelligence", router.setIntelligence)
	mux.HandleFunc("/v1/models", router.models)
	mux.HandleFunc("/v1/chat/completions", router.chatCompletions)

	server := &http.Server{
		Addr:              router.config.Listen,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("router listening on %s", router.config.Listen)
	log.Fatal(server.ListenAndServe())
}

func NewRouter(configPath, keysPath string) (*Router, error) {
	configPath = expandPath(configPath)
	keysPath = expandPath(keysPath)

	var cfg Config
	if err := readJSON(configPath, &cfg); err != nil {
		return nil, err
	}
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:8080"
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 120000
	}

	keys := KeysFile{Keys: map[string]string{}}
	if err := readJSON(keysPath, &keys); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if keys.Keys == nil {
		keys.Keys = map[string]string{}
	}

	router := &Router{
		config:    cfg,
		keys:      keys.Keys,
		configDir: filepath.Dir(configPath),
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond,
		},
		active: map[string]string{},
	}
	router.initActiveModels()
	return router, nil
}

func (r *Router) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (r *Router) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, r.modelState())
}

func (r *Router) model(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, http.StatusOK, r.modelState())
}

func (r *Router) modelState() StatusResponse {
	r.mu.RLock()
	active := cloneStringMap(r.active)
	current := r.current
	r.mu.RUnlock()

	providers := make(map[string]string, len(r.config.Providers))
	models := make([]StatusModel, 0)
	currentModel := active[current]
	currentProvider := ""
	for providerName, provider := range r.config.Providers {
		state := "disabled"
		if provider.Enabled {
			state = "enabled"
		}
		providers[providerName] = state
		for modelName, model := range provider.Models {
			class := normalizedClass(model.Class)
			models = append(models, StatusModel{
				Name:     modelName,
				Class:    class,
				Provider: providerName,
				Enabled:  provider.Enabled && model.Enabled,
				Active:   active[class] == modelName,
			})
			if provider.Enabled && model.Enabled && modelName == currentModel {
				currentProvider = providerName
			}
		}
	}

	return StatusResponse{
		Status:              "ok",
		Listen:              r.config.Listen,
		CurrentIntelligence: current,
		CurrentModel:        currentModel,
		CurrentProvider:     currentProvider,
		Active:              active,
		Providers:           providers,
		Models:              models,
	}
}

func (r *Router) setModel(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var body SetModelRequest
	if err := decodeSmallJSON(w, req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if body.Model == "" {
		body.Model = req.URL.Query().Get("model")
	}
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	choice, err := r.findSpecificModel(body.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}
	if choice.Class == "" {
		writeError(w, http.StatusBadRequest, "invalid_model", fmt.Sprintf("model %q must be configured as smart or dumb", body.Model))
		return
	}

	r.setActive(choice.Class, choice.PublicModel)
	r.setCurrent(choice.Class)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "ok",
		"intelligence": choice.Class,
		"model":        choice.PublicModel,
		"provider":     choice.ProviderName,
	})
}

func (r *Router) setIntelligence(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	var body SetIntelligenceRequest
	if err := decodeSmallJSON(w, req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	intelligence := body.Intelligence
	if intelligence == "" {
		intelligence = body.Class
	}
	if intelligence == "" {
		intelligence = req.URL.Query().Get("intelligence")
	}
	if intelligence == "" {
		intelligence = req.URL.Query().Get("class")
	}

	class := normalizedClass(intelligence)
	if class == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "intelligence must be smart or dumb")
		return
	}
	if r.getActive(class) == "" {
		choices := r.classChoices(class)
		if len(choices) == 0 {
			writeError(w, http.StatusBadRequest, "model_not_found", fmt.Sprintf("no enabled %q model", class))
			return
		}
		r.setActive(class, choices[0].PublicModel)
	}
	r.setCurrent(class)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "ok",
		"intelligence": class,
		"model":        r.getActive(class),
	})
}

func (r *Router) models(w http.ResponseWriter, _ *http.Request) {
	data := make([]OpenAIModel, 0)
	for providerName, provider := range r.config.Providers {
		if !provider.Enabled {
			continue
		}
		for modelName, model := range provider.Models {
			if model.Enabled {
				data = append(data, OpenAIModel{ID: modelName, Object: "model", OwnedBy: providerName})
			}
		}
	}
	writeJSON(w, http.StatusOK, OpenAIModelsResponse{Object: "list", Data: data})
}

func (r *Router) chatCompletions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 64<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	var chatReq ChatRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(chatReq.Model) == "" {
		chatReq.Model = r.getCurrent()
	}

	choices, err := r.chooseModels(chatReq.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, "model_not_found", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), time.Duration(r.config.TimeoutMS)*time.Millisecond)
	defer cancel()

	var lastErr error
	var lastStatus int
	for _, choice := range choices {
		attemptReq := chatReq
		attemptReq.Model = choice.UpstreamModel

		var upstream *http.Response
		switch normalizeType(choice.Provider.Type) {
		case "anthropic_api", "anthropic":
			upstream, err = r.callAnthropic(ctx, choice, attemptReq)
		default:
			var rewritten []byte
			rewritten, err = json.Marshal(attemptReq)
			if err == nil {
				upstream, err = r.callOpenAICompatible(ctx, choice, rewritten)
			}
		}
		if err != nil {
			lastErr = err
			if choice.Class != "" {
				r.failOver(choice.Class, choice.PublicModel)
			}
			continue
		}
		if shouldFailOver(upstream.StatusCode) && len(choices) > 1 {
			lastStatus = upstream.StatusCode
			_, _ = io.Copy(io.Discard, upstream.Body)
			upstream.Body.Close()
			if choice.Class != "" {
				r.failOver(choice.Class, choice.PublicModel)
			}
			continue
		}
		defer upstream.Body.Close()
		if upstream.StatusCode >= 200 && upstream.StatusCode < 300 && choice.Class != "" {
			r.setActive(choice.Class, choice.PublicModel)
		}
		copyResponse(w, upstream)
		return
	}

	if lastErr != nil {
		writeError(w, http.StatusBadGateway, "upstream_error", lastErr.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "upstream_exhausted", fmt.Sprintf("all matching providers failed; last status was %d", lastStatus))
}

func (r *Router) chooseModels(model string) ([]ModelChoice, error) {
	wantClass := ""
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "smart":
		wantClass = "smart"
	case "dumb":
		wantClass = "dumb"
	}

	if wantClass == "" {
		choice, err := r.findSpecificModel(model)
		if err != nil {
			return nil, err
		}
		return append([]ModelChoice{choice}, r.sameClassFallbacks(choice.Class, choice.PublicModel)...), nil
	}

	choices := r.classChoices(wantClass)
	if len(choices) == 0 {
		return nil, fmt.Errorf("no enabled %q model", wantClass)
	}
	active := r.getActive(wantClass)
	return orderChoices(choices, active), nil
}

func (r *Router) findSpecificModel(model string) (ModelChoice, error) {
	for providerName, provider := range r.config.Providers {
		if !provider.Enabled {
			continue
		}
		if cfg, ok := provider.Models[model]; ok && cfg.Enabled {
			return buildChoice(providerName, provider, model, cfg), nil
		}
	}
	return ModelChoice{}, fmt.Errorf("model %q is not enabled", model)
}

func (r *Router) classChoices(class string) []ModelChoice {
	var choices []ModelChoice
	for providerName, provider := range r.config.Providers {
		if !provider.Enabled {
			continue
		}
		for publicName, cfg := range provider.Models {
			if cfg.Enabled && normalizedClass(cfg.Class) == class {
				choices = append(choices, buildChoice(providerName, provider, publicName, cfg))
			}
		}
	}
	return choices
}

func (r *Router) sameClassFallbacks(class, exclude string) []ModelChoice {
	var out []ModelChoice
	for _, choice := range orderChoices(r.classChoices(class), r.getActive(class)) {
		if choice.PublicModel != exclude {
			out = append(out, choice)
		}
	}
	return out
}

func buildChoice(providerName string, provider ProviderConfig, publicName string, cfg ModelConfig) ModelChoice {
	upstream := cfg.UpstreamModel
	if upstream == "" {
		upstream = publicName
	}
	return ModelChoice{
		PublicModel:   publicName,
		UpstreamModel: upstream,
		Class:         normalizedClass(cfg.Class),
		ProviderName:  providerName,
		Provider:      provider,
	}
}

func orderChoices(choices []ModelChoice, first string) []ModelChoice {
	if first == "" || len(choices) < 2 {
		return choices
	}
	out := make([]ModelChoice, 0, len(choices))
	for _, choice := range choices {
		if choice.PublicModel == first {
			out = append(out, choice)
			break
		}
	}
	for _, choice := range choices {
		if choice.PublicModel != first {
			out = append(out, choice)
		}
	}
	return out
}

func (r *Router) initActiveModels() {
	for _, class := range []string{"smart", "dumb"} {
		choices := r.classChoices(class)
		if len(choices) > 0 {
			r.active[class] = choices[0].PublicModel
			if r.current == "" {
				r.current = class
			}
		}
	}
}

func (r *Router) getCurrent() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

func (r *Router) setCurrent(class string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = class
}

func (r *Router) getActive(class string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active[class]
}

func (r *Router) setActive(class, model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[class] = model
}

func (r *Router) failOver(class, failedModel string) {
	choices := r.classChoices(class)
	for _, choice := range orderChoices(choices, failedModel) {
		if choice.PublicModel != failedModel {
			r.setActive(class, choice.PublicModel)
			log.Printf("class %s failed over from %s to %s", class, failedModel, choice.PublicModel)
			return
		}
	}
}

func (r *Router) callOpenAICompatible(ctx context.Context, choice ModelChoice, body []byte) (*http.Response, error) {
	endpoint, err := r.providerBaseURL(choice.Provider)
	if err != nil {
		return nil, err
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if err := r.applyAuth(req, choice.Provider); err != nil {
		return nil, err
	}
	for key, value := range choice.Provider.Headers {
		req.Header.Set(key, value)
	}
	return r.httpClient.Do(req)
}

func (r *Router) callAnthropic(ctx context.Context, choice ModelChoice, chatReq ChatRequest) (*http.Response, error) {
	if chatReq.Stream {
		return nil, errors.New("anthropic streaming translation is not implemented")
	}
	apiKey, err := r.apiKey(choice.Provider)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(choice.Provider.BaseURL, "/")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1"
	}
	endpoint += "/messages"

	anthropicReq := map[string]interface{}{
		"model":      chatReq.Model,
		"messages":   flattenSystemMessages(chatReq.Messages),
		"max_tokens": chatReq.MaxTokens,
	}
	if chatReq.MaxTokens == 0 {
		anthropicReq["max_tokens"] = 4096
	}
	if system := collectSystem(chatReq.Messages); system != "" {
		anthropicReq["system"] = system
	}
	if chatReq.Temperature != nil {
		anthropicReq["temperature"] = *chatReq.Temperature
	}
	if chatReq.TopP != nil {
		anthropicReq["top_p"] = *chatReq.TopP
	}
	payload, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Anthropic-Version", headerDefault(choice.Provider.Headers, "Anthropic-Version", "2023-06-01"))
	for key, value := range choice.Provider.Headers {
		req.Header.Set(key, value)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	translated, err := translateAnthropic(raw, choice.PublicModel)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(translated)),
	}, nil
}

func (r *Router) providerBaseURL(provider ProviderConfig) (string, error) {
	if provider.BaseURL != "" {
		return strings.TrimRight(provider.BaseURL, "/"), nil
	}

	switch normalizeType(provider.Type) {
	case "openai_api", "openai":
		return "https://api.openai.com/v1", nil
	case "deepseek":
		return "https://api.deepseek.com/v1", nil
	case "openrouter":
		return "https://openrouter.ai/api/v1", nil
	case "google_vertex", "vertex":
		if provider.ProjectID == "" || provider.Location == "" {
			return "", errors.New("google_vertex requires project_id and location when base_url is omitted")
		}
		return fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", url.PathEscape(provider.ProjectID), url.PathEscape(provider.Location)), nil
	case "codex_oauth2", "codex", "google_antigravity_oauth2", "antigravity":
		return "", fmt.Errorf("%s requires base_url because this OAuth2 provider has no stable public default endpoint", provider.Type)
	default:
		return "", fmt.Errorf("provider type %q requires base_url", provider.Type)
	}
}

func (r *Router) applyAuth(req *http.Request, provider ProviderConfig) error {
	switch normalizeType(provider.Type) {
	case "codex_oauth2", "codex", "google_antigravity_oauth2", "antigravity", "google_vertex", "vertex":
		token, err := r.oauthBearer(provider)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	default:
		key, err := r.apiKey(provider)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return nil
}

func (r *Router) apiKey(provider ProviderConfig) (string, error) {
	if provider.APIKeyRef == "" {
		return "", fmt.Errorf("provider type %q requires api_key_ref", provider.Type)
	}
	key := r.keys[provider.APIKeyRef]
	if key == "" {
		return "", fmt.Errorf("api key %q is missing from keys file", provider.APIKeyRef)
	}
	return key, nil
}

func (r *Router) oauthBearer(provider ProviderConfig) (string, error) {
	if provider.AuthFile == "" {
		return "", fmt.Errorf("provider type %q requires auth_file", provider.Type)
	}
	path := provider.AuthFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.configDir, "auth", provider.AuthFile)
	}

	var raw map[string]interface{}
	if err := readJSON(path, &raw); err != nil {
		return "", err
	}
	for _, key := range []string{"access_token", "accessToken", "id_token", "token"} {
		if value, ok := raw[key].(string); ok && value != "" {
			return value, nil
		}
	}
	if credentials, ok := raw["credentials"].(map[string]interface{}); ok {
		if value, ok := credentials["access_token"].(string); ok && value != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("auth file %s does not contain access_token, accessToken, id_token, or token", path)
}

func flattenSystemMessages(messages []ChatMessage) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "system" {
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "assistant"
		} else {
			role = "user"
		}
		out = append(out, map[string]interface{}{"role": role, "content": msg.Content})
	}
	return out
}

func collectSystem(messages []ChatMessage) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == "system" {
			parts = append(parts, contentToString(msg.Content))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func contentToString(content interface{}) string {
	switch value := content.(type) {
	case string:
		return value
	case []interface{}:
		var parts []string
		for _, item := range value {
			object, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if object["type"] == "text" {
				if text, ok := object["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprint(value)
	}
}

func defaultConfigPath(name string) string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		homeDir, homeErr := os.UserHomeDir()
		if homeErr != nil || homeDir == "" {
			return filepath.Join(".config", "router", name)
		}
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "router", name)
}

func expandPath(path string) string {
	if path == "~" {
		homeDir, err := os.UserHomeDir()
		if err == nil && homeDir != "" {
			return homeDir
		}
	}
	if strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err == nil && homeDir != "" {
			return filepath.Join(homeDir, path[2:])
		}
	}
	return path
}

func translateAnthropic(raw []byte, publicModel string) ([]byte, error) {
	var resp AnthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	var parts []string
	for _, item := range resp.Content {
		if item.Type == "text" {
			parts = append(parts, item.Text)
		}
	}
	finishReason := "stop"
	if resp.StopReason == "max_tokens" {
		finishReason = "length"
	}
	out := map[string]interface{}{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   publicModel,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"finish_reason": finishReason,
				"message": map[string]string{
					"role":    "assistant",
					"content": strings.Join(parts, ""),
				},
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	return json.Marshal(out)
}

func readJSON(path string, out interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func copyResponse(w http.ResponseWriter, upstream *http.Response) {
	for key, values := range upstream.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	contentType := upstream.Header.Get("Content-Type")
	if contentType != "" {
		if mediatype, _, err := mime.ParseMediaType(contentType); err == nil {
			w.Header().Set("Content-Type", mediatype)
		}
	}
	w.WriteHeader(upstream.StatusCode)
	_, _ = io.Copy(w, upstream.Body)
}

func decodeSmallJSON(w http.ResponseWriter, req *http.Request, out interface{}) error {
	if req.Body == nil || req.ContentLength == 0 {
		return nil
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20))
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]interface{}{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func headerDefault(headers map[string]string, key, fallback string) string {
	for existing, value := range headers {
		if strings.EqualFold(existing, key) && value != "" {
			return value
		}
	}
	return fallback
}

func normalizeType(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
}

func normalizedClass(value string) string {
	class := strings.ToLower(strings.TrimSpace(value))
	if class == "smart" || class == "dumb" {
		return class
	}
	return ""
}

func shouldFailOver(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusPaymentRequired ||
		status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status >= 500
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
