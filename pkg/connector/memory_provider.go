package connector

import (
	"fmt"
	"strings"

	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/memory/embedding"
)

type memoryProviderResult struct {
	Provider    memory.EmbeddingProvider
	Status      memory.ProviderStatus
	ProviderKey string
	BaseURL     string
	Headers     map[string]string
}

type providerConfig struct {
	provider memory.EmbeddingProvider
	status   memory.ProviderStatus
	baseURL  string
	headers  map[string]string
}

func buildMemoryProvider(client *AIClient, cfg *memory.ResolvedConfig) (*memoryProviderResult, error) {
	if client == nil || cfg == nil {
		return nil, fmt.Errorf("memory provider requires client and config")
	}
	requested := strings.TrimSpace(cfg.Provider)
	if requested == "" {
		requested = "auto"
	}
	fallback := strings.TrimSpace(cfg.Fallback)
	if fallback == "" {
		fallback = "none"
	}

	createProvider := func(kind string) (*providerConfig, error) {
		switch kind {
		case "gemini":
			apiKey, baseURL, headers := resolveGeminiEmbeddingConfig(client, cfg)
			provider, err := embedding.NewGeminiProvider(apiKey, baseURL, cfg.Model, headers)
			if err != nil {
				return nil, err
			}
			return &providerConfig{
				provider: provider,
				status: memory.ProviderStatus{
					Provider: provider.ID(),
					Model:    provider.Model(),
				},
				baseURL: baseURL,
				headers: headers,
			}, nil
		case "openai":
			apiKey, baseURL, headers := resolveOpenAIEmbeddingConfig(client, cfg)
			provider, err := embedding.NewOpenAIProvider(apiKey, baseURL, cfg.Model, headers)
			if err != nil {
				return nil, err
			}
			return &providerConfig{
				provider: provider,
				status: memory.ProviderStatus{
					Provider: provider.ID(),
					Model:    provider.Model(),
				},
				baseURL: baseURL,
				headers: headers,
			}, nil
		default:
			return nil, fmt.Errorf("unsupported embeddings provider: %s", kind)
		}
	}

	if requested == "auto" {
		if hasOpenAIEmbeddingConfig(client, cfg) {
			if provider, err := createProvider("openai"); err == nil {
				return finalizeProvider(cfg, provider), nil
			}
		}
		if hasGeminiEmbeddingConfig(cfg) {
			if provider, err := createProvider("gemini"); err == nil {
				return finalizeProvider(cfg, provider), nil
			}
		}
		return nil, fmt.Errorf("no embeddings provider available")
	}

	primary, err := createProvider(requested)
	if err == nil {
		return finalizeProvider(cfg, primary), nil
	}

	if fallback != "" && fallback != "none" && fallback != requested {
		fallbackProvider, fallbackErr := createProvider(fallback)
		if fallbackErr == nil {
			fallbackProvider.status.Fallback = &memory.FallbackStatus{
				From:   requested,
				Reason: err.Error(),
			}
			return finalizeProvider(cfg, fallbackProvider), nil
		}
		return nil, fmt.Errorf("%v; fallback to %s failed: %v", err, fallback, fallbackErr)
	}
	return nil, err
}

func finalizeProvider(cfg *memory.ResolvedConfig, provider *providerConfig) *memoryProviderResult {
	providerKey := memory.ComputeProviderKey(
		provider.provider.ID(),
		provider.provider.Model(),
		provider.baseURL,
		provider.headers,
	)
	return &memoryProviderResult{
		Provider:    provider.provider,
		Status:      provider.status,
		ProviderKey: providerKey,
		BaseURL:     provider.baseURL,
		Headers:     provider.headers,
	}
}

func resolveOpenAIEmbeddingConfig(client *AIClient, cfg *memory.ResolvedConfig) (string, string, map[string]string) {
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
	headers := cfg.Remote.Headers
	return apiKey, baseURL, headers
}

func resolveGeminiEmbeddingConfig(client *AIClient, cfg *memory.ResolvedConfig) (string, string, map[string]string) {
	apiKey := strings.TrimSpace(cfg.Remote.APIKey)
	baseURL := strings.TrimSpace(cfg.Remote.BaseURL)
	if baseURL == "" {
		baseURL = embedding.DefaultGeminiBaseURL
	}
	headers := cfg.Remote.Headers
	return apiKey, baseURL, headers
}

func hasOpenAIEmbeddingConfig(client *AIClient, cfg *memory.ResolvedConfig) bool {
	apiKey, _, _ := resolveOpenAIEmbeddingConfig(client, cfg)
	return strings.TrimSpace(apiKey) != ""
}

func hasGeminiEmbeddingConfig(cfg *memory.ResolvedConfig) bool {
	return strings.TrimSpace(cfg.Remote.APIKey) != ""
}
