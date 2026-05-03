package ai

import (
	"net/url"
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/stringutil"
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

func (oc *OpenAIConnector) modelProviderConfig(provider string) ModelProviderConfig {
	if oc == nil || oc.Config.Models == nil {
		return ModelProviderConfig{}
	}
	return oc.Config.Models.Provider(provider)
}

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

func (oc *OpenAIConnector) resolveServiceConfig(provider string, cfg *aiLoginConfig) ServiceConfigMap {
	services := ServiceConfigMap{}

	if provider == ProviderMagicProxy {
		base := normalizeProxyBaseURL(loginCredentialBaseURL(cfg))
		if base != "" {
			token := trimToken(loginCredentialAPIKey(cfg))
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
		BaseURL: func() string {
			base := strings.TrimSpace(oc.modelProviderConfig(ProviderOpenAI).BaseURL)
			if base == "" {
				base = defaultOpenAIBaseURL
			}
			return strings.TrimRight(base, "/")
		}(),
		APIKey: func() string {
			if key := trimToken(oc.modelProviderConfig(ProviderOpenAI).APIKey); key != "" {
				return key
			}
			return loginTokenForService(provider, cfg, serviceOpenAI)
		}(),
	}
	services[serviceOpenRouter] = ServiceConfig{
		BaseURL: func() string {
			base := strings.TrimSpace(oc.modelProviderConfig(ProviderOpenRouter).BaseURL)
			if base == "" {
				base = defaultOpenRouterBaseURL
			}
			return strings.TrimRight(base, "/")
		}(),
		APIKey: func() string {
			if key := trimToken(oc.modelProviderConfig(ProviderOpenRouter).APIKey); key != "" {
				return key
			}
			return loginTokenForService(provider, cfg, serviceOpenRouter)
		}(),
	}
	services[serviceExa] = ServiceConfig{
		APIKey: loginTokenForService(provider, cfg, serviceExa),
	}
	return services
}

func (oc *OpenAIConnector) resolveProviderAPIKeyForConfig(provider string, cfg *aiLoginConfig) string {
	switch provider {
	case ProviderMagicProxy:
		if key := trimToken(loginCredentialAPIKey(cfg)); key != "" {
			return key
		}
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.OpenRouter)
		}
	case ProviderOpenRouter:
		if key := trimToken(oc.modelProviderConfig(ProviderOpenRouter).APIKey); key != "" {
			return key
		}
		if key := trimToken(loginCredentialAPIKey(cfg)); key != "" {
			return key
		}
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.OpenRouter)
		}
	case ProviderOpenAI:
		if key := trimToken(oc.modelProviderConfig(ProviderOpenAI).APIKey); key != "" {
			return key
		}
		if key := trimToken(loginCredentialAPIKey(cfg)); key != "" {
			return key
		}
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.OpenAI)
		}
	default:
		return trimToken(loginCredentialAPIKey(cfg))
	}
	return ""
}

func loginTokenForService(provider string, cfg *aiLoginConfig, service string) string {
	switch service {
	case serviceOpenAI:
		if provider == ProviderOpenAI {
			return trimToken(loginCredentialAPIKey(cfg))
		}
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.OpenAI)
		}
	case serviceOpenRouter:
		if provider == ProviderOpenRouter || provider == ProviderMagicProxy {
			return trimToken(loginCredentialAPIKey(cfg))
		}
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.OpenRouter)
		}
	case serviceExa:
		if tokens := loginCredentialServiceTokens(cfg); tokens != nil {
			return trimToken(tokens.Exa)
		}
	}
	return ""
}
