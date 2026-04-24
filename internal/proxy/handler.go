package proxy

import (
	"bytes"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"ai-gateway/internal/config"
	"ai-gateway/internal/provider"
)

//go:embed web
var webFS embed.FS

// Request limiter based on server config
func maxBodySize() int64 {
	return config.GlobalConfig.Server.MaxBodySize
}

// checkAuth validates Authorization header against server.auth_tokens
func checkAuth(r *http.Request) bool {
	tokens := config.GlobalConfig.Server.AuthTokens
	if len(tokens) == 0 {
		return true // auth disabled
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	for _, t := range tokens {
		if t == token {
			return true
		}
	}
	return false
}

// newHTTPClient creates a configured HTTP client with proper timeouts
func newHTTPClient(streaming bool) *http.Client {
	timeouts := config.GlobalConfig.Server.Timeouts
	if timeouts.Connect == 0 {
		timeouts.Connect = 10
	}
	if timeouts.Request == 0 {
		timeouts.Request = 300
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(timeouts.Connect) * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}

	client := &http.Client{
		Transport: transport,
	}

	// Only set total timeout for non-streaming requests
	if !streaming {
		client.Timeout = time.Duration(timeouts.Request) * time.Second
	}

	return client
}

// writeOpenAIError writes an OpenAI-style error response
func writeOpenAIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
			"type":    code,
		},
	})
}

// Handler proxies OpenAI-compatible requests to upstream providers
type Handler struct{}

// NewHandler creates a new proxy handler
func NewHandler() *Handler {
	return &Handler{}
}

// ServeHTTP handles all incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("[handler] panic recovered: %v", err)
			writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}
	}()

	// Set max body size for all routes
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize())

	path := r.URL.Path
	log.Printf("[%s] %s", r.Method, path)

	// Auth check for API routes (skip health and web UI)
	if path != "/health" && !strings.HasPrefix(path, "/web") {
		if !checkAuth(r) {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			writeOpenAIError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid authorization")
			return
		}
	}

	// Web UI + web API routes
	if strings.HasPrefix(path, "/web") {
		h.handleWeb(w, r)
		return
	}

	switch {
	case path == "/v1/chat/completions":
		h.handleChatCompletions(w, r)
	case path == "/v1/models":
		h.handleModels(w, r)
	case path == "/health":
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	default:
		writeOpenAIError(w, http.StatusNotFound, "not_found", "endpoint not found: "+path)
	}
}

// handleWeb handles web UI and web API routes
func (h *Handler) handleWeb(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/web-api/models" {
		models := config.ListModelsDetailed()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": models,
		})
		return
	}

	if path == "/web-api/chat" && r.Method == http.MethodPost {
		h.handleWebChat(w, r)
		return
	}

	if path == "/web" || path == "/web/" {
		h.serveWebFile(w, r)
		return
	}

	if strings.HasPrefix(path, "/web/") {
		h.serveWebFile(w, r)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

func (h *Handler) serveWebFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/web" || p == "/web/" {
		p = "web/index.html"
	} else if len(p) > 5 {
		p = "web" + p[5:]
	} else {
		p = "web/index.html"
	}

	data, err := webFS.ReadFile(p)
	if err != nil {
		if len(p) > 4 && !strings.HasSuffix(p, "/") {
			data, err = webFS.ReadFile(p + "/index.html")
		}
		if err != nil {
			http.Error(w, "file not found: "+p, http.StatusNotFound)
			return
		}
	}

	if strings.HasSuffix(p, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else if strings.HasSuffix(p, ".js") {
		w.Header().Set("Content-Type", "application/javascript")
	} else if strings.HasSuffix(p, ".css") {
		w.Header().Set("Content-Type", "text/css")
	}
	w.Write(data)
}

// handleWebChat handles SSE streaming for web UI
func (h *Handler) handleWebChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	model, _ := body["model"].(string)
	messagesRaw, _ := body["messages"].([]interface{})

	if model == "" || messagesRaw == nil {
		http.Error(w, "model and messages are required", http.StatusBadRequest)
		return
	}

	// Build messages array
	messages := make([]map[string]string, 0, len(messagesRaw))
	for _, m := range messagesRaw {
		if mm, ok := m.(map[string]interface{}); ok {
			role, _ := mm["role"].(string)
			content, _ := mm["content"].(string)
			messages = append(messages, map[string]string{"role": role, "content": content})
		}
	}

	// Resolve model (handles alias)
	resolved, ok := config.ResolveModel(model)
	if !ok {
		http.Error(w, "model not found: "+model, http.StatusNotFound)
		return
	}

	p, found := provider.Get(resolved.ProviderType)
	if !found {
		http.Error(w, "no provider for type: "+resolved.ProviderType, http.StatusInternalServerError)
		return
	}

	upstreamBody := map[string]interface{}{
		"model":    resolved.UpstreamModel,
		"messages": messages,
		"stream":   true,
	}
	if temp, ok := body["temperature"].(float64); ok {
		upstreamBody["temperature"] = temp
	}
	if maxTok, ok := body["max_tokens"].(float64); ok {
		upstreamBody["max_tokens"] = int(maxTok)
	}

	upstreamBodyBytes, _ := json.Marshal(upstreamBody)
	upstreamURL := p.BuildURL(resolved.UpstreamModel, provider.ProviderInfo{
		APIKey:  resolved.APIKey,
		BaseURL: resolved.BaseURL,
	})

	upstreamReq, err := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(upstreamBodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}

	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("X-Provider-APIKey", resolved.APIKey)

	upstreamReq, err = p.BuildRequest(upstreamReq, upstreamBody)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	httpClient := newHTTPClient(true)
	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		log.Printf("[web-chat] upstream error: %v", err)
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[web-chat] upstream status %d: %s", resp.StatusCode, string(respBody))
		http.Error(w, fmt.Sprintf("upstream error: %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			break
		}
	}
}

// handleChatCompletions handles /v1/chat/completions
func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "bad_request", "failed to read body")
		return
	}
	defer r.Body.Close()

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_json", "invalid json body")
		return
	}

	model, ok := body["model"].(string)
	if !ok || model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "missing_model", "model is required")
		return
	}

	// Resolve model (supports alias)
	resolved, ok := config.ResolveModel(model)
	if !ok {
		writeOpenAIError(w, http.StatusNotFound, "model_not_found", "model not found: "+model)
		return
	}

	// Replace body model with upstream model name
	body["model"] = resolved.UpstreamModel
	bodyBytes, _ = json.Marshal(body)

	p, found := provider.Get(resolved.ProviderType)
	if !found {
		writeOpenAIError(w, http.StatusInternalServerError, "provider_not_found", "no provider for type: "+resolved.ProviderType)
		return
	}

	upstreamURL := p.BuildURL(resolved.UpstreamModel, provider.ProviderInfo{
		APIKey:  resolved.APIKey,
		BaseURL: resolved.BaseURL,
	})

	upstreamReq, err := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "failed to create request")
		return
	}

	for k, v := range r.Header {
		if k == "Content-Type" || k == "Authorization" {
			upstreamReq.Header[k] = v
		}
	}

	upstreamReq.Header.Set("X-Provider-APIKey", resolved.APIKey)

	upstreamReq, err = p.BuildRequest(upstreamReq, body)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "failed to build request: "+err.Error())
		return
	}

	streaming := isStreamingRequest(r, body)
	httpClient := newHTTPClient(streaming)
	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		log.Printf("[proxy] upstream error: %v", err)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	if streaming {
		h.handleStreaming(w, resp)
	} else {
		h.handleBuffered(w, resp)
	}
}

func isStreamingRequest(r *http.Request, body map[string]interface{}) bool {
	if stream, ok := body["stream"].(bool); ok && stream {
		return true
	}
	ae := r.Header.Get("Accept")
	return strings.Contains(ae, "text/event-stream") ||
		strings.Contains(ae, "stream-json")
}

func (h *Handler) handleBuffered(w http.ResponseWriter, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "failed to read response")
		return
	}

	for k, v := range resp.Header {
		if k == "Content-Type" || k == "Transfer-Encoding" {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

func (h *Handler) handleStreaming(w http.ResponseWriter, resp *http.Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.handleBuffered(w, resp)
		return
	}

	for k, v := range resp.Header {
		if k == "Content-Type" {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			break
		}
	}
}

// handleModels returns all models including aliases
func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	models := config.ListAllModelIDs()

	response := map[string]interface{}{
		"object": "list",
		"data": func() []map[string]string {
			result := make([]map[string]string, 0, len(models))
			for _, m := range models {
				result = append(result, map[string]string{
					"id":       m,
					"object":   "model",
					"created":  "0",
					"owned_by": "unknown",
				})
			}
			return result
		}(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
