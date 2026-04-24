package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root config
type Config struct {
	Port      int                        `yaml:"port"`
	Server    ServerConfig               `yaml:"server"`
	Providers map[string]ProviderConfig  `yaml:"providers"`
	Aliases   map[string]AliasConfig     `yaml:"aliases"`
}

// ServerConfig holds server-level settings
type ServerConfig struct {
	AuthTokens  []string `yaml:"auth_tokens"`
	MaxBodySize int64    `yaml:"max_body_size"`
	Timeouts    struct {
		Connect int `yaml:"connect"`
		Request int `yaml:"request"`
	} `yaml:"timeouts"`
}

// ProviderConfig is a simple provider config
type ProviderConfig struct {
	Type    string   `yaml:"type"` // "openai" | "anthropic"
	APIKey  string   `yaml:"api_key"`
	BaseURL string   `yaml:"base_url"`
	Models  []string `yaml:"models"`
}

// AliasConfig allows model aliasing - only model name, provider inferred
type AliasConfig struct {
	Model string `yaml:"model"`
}

// ModelMap maps model name -> provider info
type ModelMap struct {
	ProviderName string
	APIKey       string
	BaseURL      string
}

// ResolvedModel contains full resolution info for a model request
type ResolvedModel struct {
	RequestedModel string // "fast" or "gpt-4o"
	UpstreamModel  string // always the real model name
	ProviderName   string
	ProviderType   string
	APIKey         string
	BaseURL        string
}

// GlobalConfig holds the parsed config
var GlobalConfig Config

// modelMap caches model -> routing info
var modelMap = make(map[string]ModelMap)

// Load reads and parses config.yaml using standard yaml.Unmarshal
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}

	GlobalConfig = cfg

	// Build model lookup map
	modelMap = make(map[string]ModelMap)
	for pName, p := range cfg.Providers {
		for _, m := range p.Models {
			modelMap[m] = ModelMap{
				ProviderName: pName,
				APIKey:       p.APIKey,
				BaseURL:      p.BaseURL,
			}
		}
	}

	// Validate aliases point to real models
	for aliasName, alias := range cfg.Aliases {
		if _, ok := modelMap[alias.Model]; !ok {
			return fmt.Errorf("alias %q points to unknown model %q", aliasName, alias.Model)
		}
	}

	// Set defaults
	if GlobalConfig.Server.MaxBodySize == 0 {
		GlobalConfig.Server.MaxBodySize = 10 * 1024 * 1024 // 10MB
	}
	if GlobalConfig.Server.Timeouts.Connect == 0 {
		GlobalConfig.Server.Timeouts.Connect = 10
	}
	if GlobalConfig.Server.Timeouts.Request == 0 {
		GlobalConfig.Server.Timeouts.Request = 300
	}

	return nil
}

// ResolveModel resolves either an alias or direct model name to routing info
func ResolveModel(model string) (ResolvedModel, bool) {
	// Check alias first - simplified: alias only contains model name
	if alias, ok := GlobalConfig.Aliases[model]; ok {
		// Find the underlying model's provider info
		info, ok := modelMap[alias.Model]
		if !ok {
			return ResolvedModel{}, false
		}

		// Provider type inherited from the real model's provider
		providerType := providerTypeFromName(info.ProviderName)

		return ResolvedModel{
			RequestedModel: model,
			UpstreamModel:  alias.Model,
			ProviderName:   info.ProviderName,
			ProviderType:   providerType,
			APIKey:         info.APIKey,
			BaseURL:        info.BaseURL,
		}, true
	}

	// Direct model lookup
	info, ok := modelMap[model]
	if !ok {
		return ResolvedModel{}, false
	}

	return ResolvedModel{
		RequestedModel: model,
		UpstreamModel:  model,
		ProviderName:   info.ProviderName,
		ProviderType:   providerTypeFromName(info.ProviderName),
		APIKey:         info.APIKey,
		BaseURL:        info.BaseURL,
	}, true
}

func providerTypeFromName(providerName string) string {
	if cfg, ok := GlobalConfig.Providers[providerName]; ok {
		if cfg.Type != "" {
			return cfg.Type
		}
	}
	// Fallback: infer from provider name
	if providerName == "anthropic" || providerName == "ark" {
		return "anthropic"
	}
	return "openai"
}

// GetModelInfo returns routing info for a model (deprecated: use ResolveModel)
func GetModelInfo(model string) (ModelMap, bool) {
	info, ok := modelMap[model]
	return info, ok
}

// ListModels returns all registered models
func ListModels() []string {
	models := make([]string, 0, len(modelMap))
	for m := range modelMap {
		models = append(models, m)
	}
	return models
}

// ModelInfo describes a single model for the web UI
type ModelInfo struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// ListModelsDetailed returns all registered models with provider info
func ListModelsDetailed() []ModelInfo {
	result := make([]ModelInfo, 0, len(modelMap))
	for m, info := range modelMap {
		result = append(result, ModelInfo{
			ID:       m,
			Provider: info.ProviderName,
		})
	}
	return result
}

// ListAllModelIDs returns real models + aliases for model listing
func ListAllModelIDs() []string {
	models := ListModels()
	for alias := range GlobalConfig.Aliases {
		models = append(models, alias)
	}
	return models
}
