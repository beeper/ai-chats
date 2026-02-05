package connector

import (
	"net/url"
	"strings"
)

const (
	serviceOpenAI     = "openai"
	serviceOpenRouter = "openrouter"
	serviceExa        = "exa"
	serviceBrave      = "brave"
	servicePerplexity = "perplexity"
)

const (
	defaultOpenAIBaseURL     = "https://api.openai.com/v1"
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
)

type ServiceConfig struct {
	BaseURL string
	APIKey  string
}

type ServiceConfigMap map[string]ServiceConfig

func trimToken(value string) string {
	return strings.TrimSpace(value)
}

func normalizeBeeperBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		return ""
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	host := strings.TrimRight(parsed.Host, "/")
	if host == "" {
		return ""
	}
	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host + beeperBasePath
}

func (oc *OpenAIConnector) resolveOpenAIBaseURL() string {
	base := strings.TrimSpace(oc.Config.Providers.OpenAI.BaseURL)
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	return strings.TrimRight(base, "/")
}

func (oc *OpenAIConnector) resolveOpenRouterBaseURL() string {
	base := strings.TrimSpace(oc.Config.Providers.OpenRouter.BaseURL)
	if base == "" {
		base = defaultOpenRouterBaseURL
	}
	return strings.TrimRight(base, "/")
}

func (oc *OpenAIConnector) resolveBeeperBaseURL(meta *UserLoginMetadata) string {
	if meta != nil {
		base := normalizeBeeperBaseURL(meta.BaseURL)
		if base != "" {
			return base
		}
	}
	return normalizeBeeperBaseURL(oc.Config.Beeper.BaseURL)
}

func (oc *OpenAIConnector) resolveBeeperToken(meta *UserLoginMetadata) string {
	if key := trimToken(oc.Config.Beeper.Token); key != "" {
		return key
	}
	if meta != nil {
		return trimToken(meta.APIKey)
	}
	return ""
}

func (oc *OpenAIConnector) resolveServiceConfig(meta *UserLoginMetadata) ServiceConfigMap {
	services := ServiceConfigMap{}
	if meta == nil {
		return services
	}

	if meta.Provider == ProviderBeeper {
		base := oc.resolveBeeperBaseURL(meta)
		if base != "" {
			base = strings.TrimRight(base, "/")
			token := oc.resolveBeeperToken(meta)
			// Exa proxy routing is temporarily disabled; rely on explicit Exa settings.
			services[serviceOpenRouter] = ServiceConfig{
				BaseURL: base + "/openrouter/v1",
				APIKey:  token,
			}
			services[serviceOpenAI] = ServiceConfig{
				BaseURL: base + "/openai/v1",
				APIKey:  token,
			}
			services[servicePerplexity] = ServiceConfig{
				BaseURL: base + "/openrouter/v1",
				APIKey:  token,
			}
		}
		return services
	}

	services[serviceOpenAI] = ServiceConfig{
		BaseURL: oc.resolveOpenAIBaseURL(),
		APIKey:  oc.resolveOpenAIAPIKey(meta),
	}
	services[serviceOpenRouter] = ServiceConfig{
		BaseURL: oc.resolveOpenRouterBaseURL(),
		APIKey:  oc.resolveOpenRouterAPIKey(meta),
	}
	services[serviceExa] = ServiceConfig{
		APIKey: loginTokenForService(meta, serviceExa),
	}
	services[serviceBrave] = ServiceConfig{
		APIKey: loginTokenForService(meta, serviceBrave),
	}
	services[servicePerplexity] = ServiceConfig{
		APIKey: loginTokenForService(meta, servicePerplexity),
	}
	return services
}

func (oc *OpenAIConnector) resolveProviderAPIKey(meta *UserLoginMetadata) string {
	if meta == nil {
		return ""
	}
	switch meta.Provider {
	case ProviderBeeper:
		return oc.resolveBeeperToken(meta)
	case ProviderOpenRouter:
		if key := trimToken(oc.Config.Providers.OpenRouter.APIKey); key != "" {
			return key
		}
		if key := trimToken(meta.APIKey); key != "" {
			return key
		}
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.OpenRouter)
		}
	case ProviderOpenAI:
		if key := trimToken(oc.Config.Providers.OpenAI.APIKey); key != "" {
			return key
		}
		if key := trimToken(meta.APIKey); key != "" {
			return key
		}
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.OpenAI)
		}
	default:
		return trimToken(meta.APIKey)
	}
	return ""
}

func (oc *OpenAIConnector) resolveOpenAIAPIKey(meta *UserLoginMetadata) string {
	if key := trimToken(oc.Config.Providers.OpenAI.APIKey); key != "" {
		return key
	}
	if meta == nil {
		return ""
	}
	if meta.Provider == ProviderOpenAI {
		if key := trimToken(meta.APIKey); key != "" {
			return key
		}
	}
	if meta.ServiceTokens != nil {
		return trimToken(meta.ServiceTokens.OpenAI)
	}
	return ""
}

func (oc *OpenAIConnector) resolveOpenRouterAPIKey(meta *UserLoginMetadata) string {
	if key := trimToken(oc.Config.Providers.OpenRouter.APIKey); key != "" {
		return key
	}
	if meta == nil {
		return ""
	}
	if meta.Provider == ProviderOpenRouter {
		if key := trimToken(meta.APIKey); key != "" {
			return key
		}
	}
	if meta.ServiceTokens != nil {
		return trimToken(meta.ServiceTokens.OpenRouter)
	}
	return ""
}

func loginTokenForService(meta *UserLoginMetadata, service string) string {
	if meta == nil {
		return ""
	}
	if meta.Provider == ProviderBeeper {
		return trimToken(meta.APIKey)
	}

	switch service {
	case serviceOpenAI:
		if meta.Provider == ProviderOpenAI {
			return trimToken(meta.APIKey)
		}
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.OpenAI)
		}
	case serviceOpenRouter:
		if meta.Provider == ProviderOpenRouter {
			return trimToken(meta.APIKey)
		}
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.OpenRouter)
		}
	case serviceExa:
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.Exa)
		}
	case serviceBrave:
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.Brave)
		}
	case servicePerplexity:
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.Perplexity)
		}
	}
	return ""
}
