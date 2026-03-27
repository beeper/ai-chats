package ai

import (
	"net/url"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const (
	serviceOpenAI     = "openai"
	serviceOpenRouter = "openrouter"
	serviceGemini     = "gemini"
	serviceExa        = "exa"
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
	trimmed := stringutil.NormalizeBaseURL(path)
	if trimmed == "" {
		return ""
	}
	for {
		changed := false
		for _, suffix := range []string{"/openrouter/v1", "/openai/v1", "/gemini/v1beta", "/exa"} {
			if rest, ok := strings.CutSuffix(trimmed, suffix); ok {
				trimmed = strings.TrimRight(rest, "/")
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
	base = stringutil.NormalizeBaseURL(base)
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
	if meta != nil {
		if raw := strings.TrimSpace(meta.BaseURL); raw != "" {
			return normalizeProxyBaseURL(raw)
		}
	}
	return ""
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

func (oc *OpenAIConnector) resolveServiceConfig(meta *UserLoginMetadata) ServiceConfigMap {
	services := ServiceConfigMap{}
	if meta == nil {
		return services
	}

	if meta.Provider == ProviderMagicProxy {
		base := normalizeProxyBaseURL(meta.BaseURL)
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
			services[serviceGemini] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/gemini/v1beta"),
				APIKey:  token,
			}
			services[serviceExa] = ServiceConfig{
				BaseURL: joinProxyPath(base, "/exa"),
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
	return services
}

func (oc *OpenAIConnector) resolveProviderAPIKey(meta *UserLoginMetadata) string {
	if meta == nil {
		return ""
	}
	switch meta.Provider {
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
	}
	return ""
}
