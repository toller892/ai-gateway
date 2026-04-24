package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root config
type Config struct {
	Port      int                         `yaml:"port"`
	Providers map[string]ProviderConfig   `yaml:"providers"`
}

// ProviderConfig is a simple provider config
type ProviderConfig struct {
	APIKey  string   `yaml:"api_key"`
	BaseURL string   `yaml:"base_url"`
	Models  []string `yaml:"models"`
}

// CustomProvider represents a single custom provider entry
type CustomProvider struct {
	Name    string   `yaml:"name"`
	APIKey  string   `yaml:"api_key"`
	BaseURL string   `yaml:"base_url"`
	Models  []string `yaml:"models"`
}

// ModelMap maps model name → provider info
type ModelMap struct {
	ProviderName string
	APIKey       string
	BaseURL      string
}

// GlobalConfig holds the parsed config
var GlobalConfig Config

// modelMap caches model → routing info
var modelMap = make(map[string]ModelMap)

// Load reads and parses config.yaml
func Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	// Parse into yaml.Node tree for flexibility
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}

	// Navigate: node (Document) → root (Mapping) → "port", "providers"
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("unexpected yaml root")
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping at root")
	}

	var port int
	providers := make(map[string]ProviderConfig)

	for i := 0; i < len(root.Content)-1; i += 2 {
		keyNode := root.Content[i]
		valNode := root.Content[i+1]
		key := keyNode.Value

		switch key {
		case "port":
			if valNode.Kind == yaml.ScalarNode {
				fmt.Sscanf(valNode.Value, "%d", &port)
			}
		case "providers":
			if valNode.Kind != yaml.MappingNode {
				return fmt.Errorf("providers must be a mapping")
			}
			for j := 0; j < len(valNode.Content)-1; j += 2 {
				pKeyNode := valNode.Content[j]
				pValNode := valNode.Content[j+1]
				pName := pKeyNode.Value

				if pValNode.Kind == yaml.SequenceNode {
					// custom provider list
					var customList []CustomProvider
					if err := pValNode.Decode(&customList); err != nil {
						return fmt.Errorf("decode custom provider %s: %w", pName, err)
					}
					for _, cp := range customList {
						providers[cp.Name] = ProviderConfig{
							APIKey:  cp.APIKey,
							BaseURL: cp.BaseURL,
							Models:  cp.Models,
						}
					}
				} else if pValNode.Kind == yaml.MappingNode {
					var pc ProviderConfig
					if err := pValNode.Decode(&pc); err != nil {
						return fmt.Errorf("decode provider %s: %w", pName, err)
					}
					providers[pName] = pc
				}
			}
		}
	}

	GlobalConfig = Config{
		Port:      port,
		Providers: providers,
	}

	// Build model lookup map
	for pName, p := range providers {
		for _, m := range p.Models {
			modelMap[m] = ModelMap{
				ProviderName: pName,
				APIKey:       p.APIKey,
				BaseURL:      p.BaseURL,
			}
		}
	}

	return nil
}

// GetModelInfo returns routing info for a model
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
