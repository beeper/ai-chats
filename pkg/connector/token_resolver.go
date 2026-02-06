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

func normalizeMagicProxyBaseURL(raw string) string {
	return normalizeProxyBaseURL(raw)
}

func normalizeProxyBaseURL(raw string) string {
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
	path := strings.TrimRight(parsed.Path, "/")
	path = stripProxyServiceSuffix(path)
	if path == "" || path == "/" {
		return scheme + "://" + host
	}
	return scheme + "://" + host + path
}

func stripProxyServiceSuffix(path string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return ""
	}
	for {
		changed := false
		for _, suffix := range []string{"/openrouter/v1", "/openai/v1", "/exa"} {
			if strings.HasSuffix(trimmed, suffix) {
				trimmed = strings.TrimRight(strings.TrimSuffix(trimmed, suffix), "/")
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	return trimmed
}

func joinProxyPath(base, suffix string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return base
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func (oc *OpenAIConnector) resolveProxyRoot(meta *UserLoginMetadata) string {
	if oc == nil {
		return ""
	}
	if meta != nil && meta.Provider == ProviderMagicProxy {
		if raw := strings.TrimSpace(meta.BaseURL); raw != "" {
			return normalizeProxyBaseURL(raw)
		}
	}
	raw := strings.TrimSpace(oc.Config.Beeper.BaseURL)
	if raw == "" && meta != nil {
		raw = strings.TrimSpace(meta.BaseURL)
	}
	return normalizeProxyBaseURL(raw)
}

func (oc *OpenAIConnector) resolveExaProxyBaseURL(meta *UserLoginMetadata) string {
	root := oc.resolveProxyRoot(meta)
	if root == "" {
		return ""
	}
	return joinProxyPath(root, "/exa")
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
		if proxyBase := oc.resolveExaProxyBaseURL(meta); proxyBase != "" {
			services[serviceExa] = ServiceConfig{
				BaseURL: proxyBase,
				APIKey:  oc.resolveBeeperToken(meta),
			}
		}
		return services
	}

	if meta.Provider == ProviderMagicProxy {
		base := normalizeMagicProxyBaseURL(meta.BaseURL)
		if base != "" {
			token := trimToken(meta.APIKey)
			services[serviceOpenRouter] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/openrouter/v1"),
				APIKey:  token,
			}
			services[serviceOpenAI] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/openai/v1"),
				APIKey:  token,
			}
			services[serviceExa] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/exa"),
				APIKey:  token,
			}
			services[servicePerplexity] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/openrouter/v1"),
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
	case ProviderMagicProxy:
		if key := trimToken(meta.APIKey); key != "" {
			return key
		}
		if meta.ServiceTokens != nil {
			return trimToken(meta.ServiceTokens.OpenRouter)
		}
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
	if meta.Provider == ProviderMagicProxy {
		return trimToken(meta.APIKey)
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
		if meta.Provider == ProviderOpenRouter || meta.Provider == ProviderMagicProxy {
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
