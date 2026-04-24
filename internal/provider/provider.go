package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider defines the interface for upstream AI providers
type Provider interface {
	Name() string
	BuildRequest(r *http.Request, body map[string]interface{}) (*http.Request, error)
	BuildURL(model string, info ProviderInfo) string
	SupportsModel(model string) bool
}

// ProviderInfo contains routing metadata
type ProviderInfo struct {
	APIKey  string
	BaseURL string
}

// All registered providers
var providers = make(map[string]Provider)

// Register adds a provider to the registry
func Register(p Provider) {
	providers[p.Name()] = p
}

// Get returns a provider by name
func Get(name string) (Provider, bool) {
	p, ok := providers[name]
	return p, ok
}

// ─── OpenAI-compatible provider ───────────────────────────────────────────

type OpenAIProvider struct{}

func (o *OpenAIProvider) Name() string { return "openai" }

func (o *OpenAIProvider) SupportsModel(model string) bool { return true }

func (o *OpenAIProvider) BuildURL(model string, info ProviderInfo) string {
	base := strings.TrimSuffix(info.BaseURL, "/")
	return fmt.Sprintf("%s/chat/completions", base)
}

func (o *OpenAIProvider) BuildRequest(r *http.Request, body map[string]interface{}) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+r.Header.Get("X-Provider-APIKey"))
	return r, nil
}

// ─── Anthropic provider ─────────────────────────────────────────────────────

type AnthropicProvider struct{}

func (a *AnthropicProvider) Name() string { return "anthropic" }

func (a *AnthropicProvider) SupportsModel(model string) bool {
	return strings.HasPrefix(model, "claude-")
}

func (a *AnthropicProvider) BuildURL(model string, info ProviderInfo) string {
	base := strings.TrimSuffix(info.BaseURL, "/")
	return fmt.Sprintf("%s/v1/messages", base)
}

func (a *AnthropicProvider) BuildRequest(r *http.Request, body map[string]interface{}) (*http.Request, error) {
	apiKey := r.Header.Get("X-Provider-APIKey")

	maxTokens := 1024
	if v, ok := body["max_tokens"].(float64); ok {
		maxTokens = int(v)
	}

	temp := 1.0
	if v, ok := body["temperature"].(float64); ok {
		temp = v
	}

	systemMsg := ""
	if v, ok := body["system"].(string); ok {
		systemMsg = v
	}

	reqBody := struct {
		Model       interface{} `json:"model"`
		Messages    interface{} `json:"messages"`
		MaxTokens   int         `json:"max_tokens"`
		Stream      interface{} `json:"stream,omitempty"`
		System      string      `json:"system,omitempty"`
		Temperature float64     `json:"temperature"`
	}{
		Model:       body["model"],
		Messages:    body["messages"],
		MaxTokens:   maxTokens,
		Stream:      body["stream"],
		System:      systemMsg,
		Temperature: temp,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	r.Body = io.NopCloser(bytes.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", apiKey)
	r.Header.Set("anthropic-version", "2023-06-01")
	r.Header.Del("Authorization")
	return r, nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func init() {
	Register(&OpenAIProvider{})
	Register(&AnthropicProvider{})
}

// DetectProvider returns the provider for a model
func DetectProvider(model string) (Provider, bool) {
	for _, p := range providers {
		if p.SupportsModel(model) {
			return p, true
		}
	}
	p, _ := Get("openai")
	return p, p != nil
}
