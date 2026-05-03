package ai

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

const (
	openRouterAppReferer = "https://www.beeper.com/ai"
	openRouterAppTitle   = "Beeper"
)

func openRouterHeaders() map[string]string {
	return map[string]string{
		"HTTP-Referer": openRouterAppReferer,
		"X-Title":      openRouterAppTitle,
	}
}

func initProviderForLoginConfig(key string, providerID string, cfg *aiLoginConfig, connector *OpenAIConnector, login *bridgev2.UserLogin, log zerolog.Logger) (*OpenAIProvider, error) {
	if strings.TrimSpace(providerID) == "" {
		return nil, errors.New("login provider is required")
	}
	switch providerID {
	case ProviderOpenRouter:
		baseURL := strings.TrimSpace(connector.modelProviderConfig(ProviderOpenRouter).BaseURL)
		if baseURL == "" {
			baseURL = defaultOpenRouterBaseURL
		}
		return initOpenRouterProvider(key, strings.TrimRight(baseURL, "/"), "", connector.defaultPDFEngineForInit(), ProviderOpenRouter, log)

	case ProviderMagicProxy:
		baseURL := normalizeProxyBaseURL(loginCredentialBaseURL(cfg))
		if baseURL == "" {
			return nil, errors.New("magic proxy base_url is required")
		}
		return initOpenRouterProvider(key, joinProxyPath(baseURL, "/openrouter/v1"), "", connector.defaultPDFEngineForInit(), ProviderMagicProxy, log)

	case ProviderOpenAI:
		openaiURL := strings.TrimSpace(connector.modelProviderConfig(ProviderOpenAI).BaseURL)
		if openaiURL == "" {
			openaiURL = defaultOpenAIBaseURL
		}
		openaiURL = strings.TrimRight(openaiURL, "/")
		log.Info().
			Str("provider", providerID).
			Str("openai_url", openaiURL).
			Msg("Initializing AI provider endpoint")
		return NewOpenAIProviderWithUserID(key, openaiURL, "", log)

	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerID)
	}
}

func (oc *OpenAIConnector) defaultPDFEngineForInit() string {
	return "mistral-ocr"
}

// initOpenRouterProvider creates an OpenRouter-compatible provider with PDF support.
func initOpenRouterProvider(key, url, userID, pdfEngine, providerName string, log zerolog.Logger) (*OpenAIProvider, error) {
	log.Info().
		Str("provider", providerName).
		Str("openrouter_url", url).
		Msg("Initializing AI provider endpoint")
	if pdfEngine == "" {
		pdfEngine = "mistral-ocr"
	}
	provider, err := NewOpenAIProviderWithPDFPlugin(key, url, userID, pdfEngine, openRouterHeaders(), log)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", providerName, err)
	}
	return provider, nil
}
