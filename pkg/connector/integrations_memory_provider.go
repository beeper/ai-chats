package connector

import (
	"strings"

	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
)

func resolveOpenAIEmbeddingConfig(client *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	var apiKey string
	var baseURL string
	if strings.TrimSpace(cfg.Remote.APIKey) != "" {
		apiKey = strings.TrimSpace(cfg.Remote.APIKey)
	} else if client != nil && client.connector != nil {
		meta := loginMetadata(client.UserLogin)
		apiKey = strings.TrimSpace(client.connector.resolveOpenAIAPIKey(meta))
		if meta != nil {
			if apiKey == "" && meta.Provider == ProviderMagicProxy {
				apiKey = strings.TrimSpace(meta.APIKey)
			}
			if apiKey == "" && meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenRouter]; ok {
					apiKey = strings.TrimSpace(svc.APIKey)
					if baseURL == "" {
						baseURL = strings.TrimSpace(svc.BaseURL)
					}
				}
			}
		}
	}
	if strings.TrimSpace(cfg.Remote.BaseURL) != "" {
		baseURL = strings.TrimSpace(cfg.Remote.BaseURL)
	}
	if baseURL == "" && client != nil && client.connector != nil {
		if meta := loginMetadata(client.UserLogin); meta != nil {
			if meta.Provider == ProviderMagicProxy {
				base := normalizeMagicProxyBaseURL(meta.BaseURL)
				if base != "" {
					baseURL = joinProxyPath(base, "/openrouter/v1")
				}
			} else if meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenRouter]; ok && strings.TrimSpace(svc.BaseURL) != "" {
					baseURL = strings.TrimSpace(svc.BaseURL)
				}
			}
		}
		if baseURL == "" {
			baseURL = client.connector.resolveOpenAIBaseURL()
		}
	}
	return apiKey, baseURL, cfg.Remote.Headers
}

// resolveDirectOpenAIEmbeddingConfig resolves the direct OpenAI endpoint
// (/openai/v1) for batch API calls that require OpenAI-specific endpoints
// like /files and /batches which OpenRouter does not support.
func resolveDirectOpenAIEmbeddingConfig(client *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	var apiKey string
	var baseURL string
	if strings.TrimSpace(cfg.Remote.APIKey) != "" {
		apiKey = strings.TrimSpace(cfg.Remote.APIKey)
	} else if client != nil && client.connector != nil {
		meta := loginMetadata(client.UserLogin)
		apiKey = strings.TrimSpace(client.connector.resolveOpenAIAPIKey(meta))
		if meta != nil {
			if apiKey == "" && meta.Provider == ProviderMagicProxy {
				apiKey = strings.TrimSpace(meta.APIKey)
			}
			if apiKey == "" && meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenAI]; ok {
					apiKey = strings.TrimSpace(svc.APIKey)
					if baseURL == "" {
						baseURL = strings.TrimSpace(svc.BaseURL)
					}
				}
			}
		}
	}
	if strings.TrimSpace(cfg.Remote.BaseURL) != "" {
		baseURL = strings.TrimSpace(cfg.Remote.BaseURL)
	}
	if baseURL == "" && client != nil && client.connector != nil {
		if meta := loginMetadata(client.UserLogin); meta != nil {
			if meta.Provider == ProviderMagicProxy {
				base := normalizeMagicProxyBaseURL(meta.BaseURL)
				if base != "" {
					baseURL = joinProxyPath(base, "/openai/v1")
				}
			} else if meta.Provider == ProviderBeeper {
				services := client.connector.resolveServiceConfig(meta)
				if svc, ok := services[serviceOpenAI]; ok && strings.TrimSpace(svc.BaseURL) != "" {
					baseURL = strings.TrimSpace(svc.BaseURL)
				}
			}
		}
		if baseURL == "" {
			baseURL = client.connector.resolveOpenAIBaseURL()
		}
	}
	return apiKey, baseURL, cfg.Remote.Headers
}

func resolveGeminiEmbeddingConfig(_ *AIClient, cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	apiKey := strings.TrimSpace(cfg.Remote.APIKey)
	baseURL := strings.TrimSpace(cfg.Remote.BaseURL)
	if baseURL == "" {
		baseURL = integrationmemory.DefaultGeminiBaseURL
	}
	return apiKey, baseURL, cfg.Remote.Headers
}
