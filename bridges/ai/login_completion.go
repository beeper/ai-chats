package ai

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func (ol *OpenAILogin) resolveLoginTarget(ctx context.Context, provider string) (networkid.UserLoginID, int, error) {
	if ol.Override != nil {
		return ol.Override.ID, 1, nil
	}

	dupCount := 0
	for _, existing := range ol.User.GetUserLogins() {
		if existing == nil || existing.Metadata == nil {
			continue
		}
		meta, ok := existing.Metadata.(*UserLoginMetadata)
		if !ok || meta == nil {
			continue
		}
		if meta.Provider == provider {
			dupCount++
		}
	}

	ordinal := dupCount + 1
	loginID := providerLoginID(provider, ol.User.MXID, ordinal)

	// Ensure uniqueness in case of gaps or concurrent additions.
	if ol.Connector != nil && ol.Connector.br != nil {
		used := map[string]struct{}{}
		for _, existing := range ol.User.GetUserLogins() {
			if existing != nil {
				used[string(existing.ID)] = struct{}{}
			}
		}
		for {
			if _, ok := used[string(loginID)]; ok {
				ordinal++
				loginID = providerLoginID(provider, ol.User.MXID, ordinal)
				continue
			}
			if existing, _ := ol.Connector.br.GetExistingUserLoginByID(ctx, loginID); existing != nil {
				used[string(loginID)] = struct{}{}
				ordinal++
				loginID = providerLoginID(provider, ol.User.MXID, ordinal)
				continue
			}
			break
		}
	}

	return loginID, ordinal, nil
}

func (ol *OpenAILogin) validateLoginMetadata(ctx context.Context, loginID networkid.UserLoginID, provider string, cfg *aiLoginConfig) error {
	if ol == nil || ol.User == nil || ol.Connector == nil {
		return nil
	}
	tempDBLogin := &database.UserLogin{
		ID:       loginID,
		UserMXID: ol.User.MXID,
		Metadata: &UserLoginMetadata{Provider: provider},
	}
	tempLogin := &bridgev2.UserLogin{
		UserLogin: tempDBLogin,
		Bridge:    ol.User.Bridge,
		User:      ol.User,
		Log:       ol.User.Log.With().Str("login_id", string(loginID)).Str("component", "ai-login-validation").Logger(),
	}
	tempClient, err := newAIClient(tempLogin, ol.Connector, ol.Connector.resolveProviderAPIKeyForConfig(provider, cfg), cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize login client: %w", err)
	}

	valCtx, valCancel := context.WithTimeout(ctx, 5*time.Second)
	defer valCancel()

	_, valErr := tempClient.provider.ListModels(valCtx)
	if valErr != nil && IsAuthError(valErr) {
		return errors.New("invalid API key: authentication failed")
	}
	return nil
}

func (ol *OpenAILogin) resolveCustomLogin(input map[string]string) (string, string, *ServiceTokens, error) {
	if input == nil {
		input = map[string]string{}
	}
	openrouterCfg := strings.TrimSpace(ol.Connector.modelProviderConfig(ProviderOpenRouter).APIKey)
	openaiCfg := strings.TrimSpace(ol.Connector.modelProviderConfig(ProviderOpenAI).APIKey)

	openrouterInput := ""
	openaiInput := ""
	if openrouterCfg == "" {
		openrouterInput = strings.TrimSpace(input["openrouter_api_key"])
	}
	if openaiCfg == "" {
		openaiInput = strings.TrimSpace(input["openai_api_key"])
	}

	openrouterToken := openrouterCfg
	if openrouterToken == "" {
		openrouterToken = openrouterInput
	}
	openaiToken := openaiCfg
	if openaiToken == "" {
		openaiToken = openaiInput
	}

	if openrouterToken == "" && openaiToken == "" {
		return "", "", nil, &ErrOpenAIOrOpenRouterRequired
	}

	preferredProvider := ""
	if ol.Override != nil {
		if overrideMeta := loginMetadata(ol.Override); overrideMeta != nil {
			preferredProvider = normalizeProvider(overrideMeta.Provider)
		}
	}

	provider := ProviderOpenAI
	apiKey := openaiToken
	switch preferredProvider {
	case ProviderOpenAI:
		if openaiToken == "" {
			return "", "", nil, &ErrOpenAIOrOpenRouterRequired
		}
	case ProviderOpenRouter:
		if openrouterToken == "" {
			return "", "", nil, &ErrOpenAIOrOpenRouterRequired
		}
		provider = ProviderOpenRouter
		apiKey = openrouterToken
	case "":
		if openrouterToken != "" {
			provider = ProviderOpenRouter
			apiKey = openrouterToken
		}
	default:
		if openrouterToken != "" {
			provider = ProviderOpenRouter
			apiKey = openrouterToken
		}
	}
	if provider == ProviderOpenAI && openaiToken == "" && openrouterToken != "" {
		provider = ProviderOpenRouter
		apiKey = openrouterToken
	}

	serviceTokens := &ServiceTokens{}

	if provider != ProviderOpenAI && openaiCfg == "" && openaiInput != "" {
		serviceTokens.OpenAI = openaiInput
	}
	if provider != ProviderOpenRouter && openrouterCfg == "" && openrouterInput != "" {
		serviceTokens.OpenRouter = openrouterInput
	}

	if !ol.configHasExaKey() {
		serviceTokens.Exa = strings.TrimSpace(input["exa_api_key"])
	}

	return provider, apiKey, serviceTokens, nil
}

func parseMagicProxyLink(raw string) (string, string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", &ErrBaseURLRequired
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return "", "", &ErrBaseURLRequired
	}
	token := strings.TrimSpace(parsed.Fragment)
	if token == "" {
		return "", "", &ErrAPIKeyRequired
	}
	scheme := strings.TrimSpace(parsed.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	baseURL := scheme + "://" + strings.TrimSpace(parsed.Host)
	if parsed.Path != "" {
		baseURL += parsed.Path
	}
	baseURL = normalizeProxyBaseURL(baseURL)
	if baseURL == "" {
		return "", "", &ErrBaseURLRequired
	}
	return baseURL, token, nil
}

func (ol *OpenAILogin) configHasOpenRouterKey() bool {
	return strings.TrimSpace(ol.Connector.modelProviderConfig(ProviderOpenRouter).APIKey) != ""
}

func (ol *OpenAILogin) configHasOpenAIKey() bool {
	return strings.TrimSpace(ol.Connector.modelProviderConfig(ProviderOpenAI).APIKey) != ""
}

func (ol *OpenAILogin) configHasExaKey() bool {
	if ol.Connector.Config.Tools.Web != nil &&
		ol.Connector.Config.Tools.Web.Search != nil &&
		strings.TrimSpace(ol.Connector.Config.Tools.Web.Search.Exa.APIKey) != "" {
		return true
	}
	if ol.Connector.Config.Tools.Web != nil &&
		ol.Connector.Config.Tools.Web.Fetch != nil &&
		strings.TrimSpace(ol.Connector.Config.Tools.Web.Fetch.Exa.APIKey) != "" {
		return true
	}
	return false
}

// formatRemoteName generates a display name for the account based on provider.
func formatRemoteName(provider, apiKey string) string {
	switch provider {
	case ProviderOpenAI:
		return fmt.Sprintf("OpenAI (%s)", maskAPIKey(apiKey))
	case ProviderOpenRouter:
		return fmt.Sprintf("OpenRouter (%s)", maskAPIKey(apiKey))
	case ProviderMagicProxy:
		return fmt.Sprintf("Magic Proxy (%s)", maskAPIKey(apiKey))
	default:
		return "AI Chats"
	}
}

func maskAPIKey(key string) string {
	if len(key) <= 6 {
		return "***"
	}
	return fmt.Sprintf("%s...%s", key[:3], key[len(key)-3:])
}
