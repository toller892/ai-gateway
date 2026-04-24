package proxy

import (
	"bytes"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"ai-gateway/internal/config"
	"ai-gateway/internal/provider"
)

//go:embed web
var webFS embed.FS

// Shared HTTP client with connection pooling, HTTP/1.1 only
// TLSNextProto set to nil disables HTTP/2
var httpClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90,
		TLSClientConfig:     &tls.Config{MaxVersion: tls.VersionTLS12},
	},
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
		}
	}()

	path := r.URL.Path
	log.Printf("[%s] %s", r.Method, path)

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
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// ─── Web UI + Web API ─────────────────────────────────────────────────────────

func (h *Handler) handleWeb(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// /web-api/models → JSON model list with provider info
	if path == "/web-api/models" {
		models := config.ListModelsDetailed()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": models,
		})
		return
	}

	// /web-api/chat → SSE streaming chat
	if path == "/web-api/chat" && r.Method == http.MethodPost {
		h.handleWebChat(w, r)
		return
	}

	// /web or /web/ → serve index.html
	if path == "/web" || path == "/web/" {
		h.serveWebFile(w, r)
		return
	}
	// /web/* → static files
	if strings.HasPrefix(path, "/web/") {
		h.serveWebFile(w, r)
		return
	}

	http.Error(w, "not found", http.StatusNotFound)
}

// serveWebFile serves files from the embedded webFS
// strips "/web" prefix and looks up files under "web/" in the FS
func (h *Handler) serveWebFile(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	// Strip "/web" prefix. "/web/foo" → "web/foo", "/web/" → "web/index.html"
	if p == "/web" || p == "/web/" {
		p = "web/index.html"
	} else if len(p) > 5 {
		p = "web" + p[5:] // skip "/web/"
	} else {
		p = "web/index.html"
	}

	data, err := webFS.ReadFile(p)
	if err != nil {
		// try index.html for directory-like paths
		if len(p) > 4 && !strings.HasSuffix(p, "/") {
			data, err = webFS.ReadFile(p + "/index.html")
		}
		if err != nil {
			http.Error(w, "file not found: "+p, http.StatusNotFound)
			return
		}
	}

	// simple mime detection
	if strings.HasSuffix(p, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else if strings.HasSuffix(p, ".js") {
		w.Header().Set("Content-Type", "application/javascript")
	} else if strings.HasSuffix(p, ".css") {
		w.Header().Set("Content-Type", "text/css")
	}
	w.Write(data)
}

// handleWebChat — SSE streaming POST /web-api/chat
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

	// Build OpenAI-compatible messages array
	messages := make([]map[string]string, 0, len(messagesRaw))
	for _, m := range messagesRaw {
		if mm, ok := m.(map[string]interface{}); ok {
			role, _ := mm["role"].(string)
			content, _ := mm["content"].(string)
			messages = append(messages, map[string]string{"role": role, "content": content})
		}
	}

	// Route to provider
	info, ok := config.GetModelInfo(model)
	if !ok {
		http.Error(w, "model not found: "+model, http.StatusNotFound)
		return
	}

	p, found := provider.DetectProvider(model)
	if !found {
		http.Error(w, "no provider for model: "+model, http.StatusInternalServerError)
		return
	}

	// Build upstream request body (OpenAI format)
	upstreamBody := map[string]interface{}{
		"model":    model,
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
	upstreamURL := p.BuildURL(model, provider.ProviderInfo{
		APIKey:  info.APIKey,
		BaseURL: info.BaseURL,
	})

	upstreamReq, err := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(upstreamBodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}

	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("X-Provider-APIKey", info.APIKey)

	upstreamReq, err = p.BuildRequest(upstreamReq, upstreamBody)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

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

	// SSE streaming
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	// Read upstream SSE stream and relay to client
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

// ─── POST /v1/chat/completions ──────────────────────────────────────────────

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body
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

	model, ok := body["model"].(string)
	if !ok || model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}

	// Route to provider
	info, ok := config.GetModelInfo(model)
	if !ok {
		http.Error(w, "model not found: "+model, http.StatusNotFound)
		return
	}

	// Detect provider type
	p, found := provider.DetectProvider(model)
	if !found {
		http.Error(w, "no provider for model: "+model, http.StatusInternalServerError)
		return
	}

	// Build upstream URL
	upstreamURL := p.BuildURL(model, provider.ProviderInfo{
		APIKey:  info.APIKey,
		BaseURL: info.BaseURL,
	})

	// Prepare upstream request
	upstreamReq, err := http.NewRequest(http.MethodPost, upstreamURL, bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}

	// Copy headers
	for k, v := range r.Header {
		if k == "Content-Type" || k == "Authorization" {
			upstreamReq.Header[k] = v
		}
	}

	// Set provider API key header (used by BuildRequest)
	upstreamReq.Header.Set("X-Provider-APIKey", info.APIKey)

	// Let provider transform the request if needed
	upstreamReq, err = p.BuildRequest(upstreamReq, body)
	if err != nil {
		http.Error(w, "failed to build request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Call upstream
	resp, err := httpClient.Do(upstreamReq)
	if err != nil {
		log.Printf("[proxy] upstream error: %v", err)
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Stream or buffered response
	if isStreaming(r) {
		h.handleStreaming(w, resp)
	} else {
		h.handleBuffered(w, resp)
	}
}

func isStreaming(r *http.Request) bool {
	ae := r.Header.Get("Accept")
	return strings.Contains(ae, "text/event-stream") ||
		strings.Contains(ae, "stream-json")
}

func (h *Handler) handleBuffered(w http.ResponseWriter, resp *http.Response) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
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

// ─── GET /v1/models ─────────────────────────────────────────────────────────

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	models := config.ListModels()

	response := map[string]interface{}{
		"object": "list",
		"data": func() []map[string]string {
			result := make([]map[string]string, 0, len(models))
			for _, m := range models {
				result = append(result, map[string]string{
					"id":      m,
					"object":  "model",
					"created": "0",
					"owned_by": "unknown",
				})
			}
			return result
		}(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
