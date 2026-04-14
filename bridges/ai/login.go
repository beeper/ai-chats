package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

// Provider constants - all use OpenAI SDK with different base URLs
const (
	ProviderOpenAI     = "openai"      // Direct OpenAI API
	ProviderOpenRouter = "openrouter"  // Direct OpenRouter API
	ProviderMagicProxy = "magic_proxy" // Magic Proxy (OpenRouter-compatible)
	FlowCustom         = "custom"      // Custom login flow (provider resolved during login)
)

var (
	_ bridgev2.LoginProcess             = (*OpenAILogin)(nil)
	_ bridgev2.LoginProcessWithOverride = (*OpenAILogin)(nil)
	_ bridgev2.LoginProcessUserInput    = (*OpenAILogin)(nil)

	errAIReloginTargetInvalid = sdk.NewLoginRespError(http.StatusBadRequest, "Invalid relogin target.", "AI", "INVALID_RELOGIN_TARGET")
	errAIMissingUserContext   = sdk.NewLoginRespError(http.StatusInternalServerError, "Missing user context for login.", "AI", "MISSING_USER_CONTEXT")
	errAIMissingReloginMeta   = sdk.NewLoginRespError(http.StatusInternalServerError, "Missing relogin metadata.", "AI", "MISSING_RELOGIN_METADATA")
)

// OpenAILogin maps a Matrix user to a synthetic OpenAI "login".
type OpenAILogin struct {
	User      *bridgev2.User
	Connector *OpenAIConnector
	FlowID    string
	Override  *bridgev2.UserLogin
}

type loginCompletionInput struct {
	Provider      string
	APIKey        string
	BaseURL       string
	ServiceTokens *ServiceTokens
}

func normalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderOpenAI:
		return ProviderOpenAI
	case ProviderOpenRouter:
		return ProviderOpenRouter
	case ProviderMagicProxy:
		return ProviderMagicProxy
	case FlowCustom:
		return FlowCustom
	default:
		return strings.TrimSpace(provider)
	}
}

func (ol *OpenAILogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return ol.runLogin(ctx, nil, nil)
}

func (ol *OpenAILogin) Cancel() {}

func (ol *OpenAILogin) StartWithOverride(ctx context.Context, old *bridgev2.UserLogin) (*bridgev2.LoginStep, error) {
	return ol.runLogin(ctx, old, nil)
}

func (ol *OpenAILogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	return ol.runLogin(ctx, nil, input)
}

func (ol *OpenAILogin) runLogin(ctx context.Context, override *bridgev2.UserLogin, input map[string]string) (*bridgev2.LoginStep, error) {
	if override != nil {
		if ol.User == nil || override.UserMXID != ol.User.MXID {
			return nil, errAIReloginTargetInvalid
		}
		ol.Override = override
	}
	resolved, step, err := ol.resolveLoginInput(input)
	if err != nil || step != nil {
		return step, err
	}
	return ol.completeLogin(ctx, *resolved)
}

func (ol *OpenAILogin) resolveLoginInput(input map[string]string) (*loginCompletionInput, *bridgev2.LoginStep, error) {
	step := ol.credentialsStep()
	if step != nil && input == nil {
		return nil, step, nil
	}

	switch ol.FlowID {
	case ProviderMagicProxy:
		link := strings.TrimSpace(input["magic_proxy_link"])
		baseURL, apiKey, err := parseMagicProxyLink(link)
		if err != nil {
			return nil, nil, err
		}
		if ol.Connector != nil && ol.Connector.br != nil {
			event := ol.Connector.br.Log.Info().
				Str("component", "ai-login").
				Str("provider", ProviderMagicProxy).
				Int("token_length", len(apiKey))
			if parsed, parseErr := url.Parse(baseURL); parseErr == nil {
				event = event.
					Str("base_url_host", parsed.Host).
					Str("base_url_path", parsed.Path)
			} else {
				event = event.Str("base_url", baseURL)
			}
			event.Msg("Resolved magic proxy login URL")
		}
		return &loginCompletionInput{
			Provider: ProviderMagicProxy,
			APIKey:   apiKey,
			BaseURL:  baseURL,
		}, nil, nil
	case FlowCustom:
		provider, apiKey, serviceTokens, err := ol.resolveCustomLogin(input)
		if err != nil {
			return nil, nil, err
		}
		return &loginCompletionInput{
			Provider:      provider,
			APIKey:        apiKey,
			ServiceTokens: serviceTokens,
		}, nil, nil
	default:
		return nil, nil, bridgev2.ErrInvalidLoginFlowID
	}
}

func (ol *OpenAILogin) credentialsStep() *bridgev2.LoginStep {
	var fields []bridgev2.LoginInputDataField
	switch ol.FlowID {
	case ProviderMagicProxy:
		fields = append(fields, bridgev2.LoginInputDataField{
			Type: bridgev2.LoginInputFieldTypeURL,
			ID:   "magic_proxy_link",
			Name: "Magic Proxy link",
		})
	case FlowCustom:
		if !ol.configHasOpenRouterKey() {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "openrouter_api_key",
				Name:        "OpenRouter API Key",
				Description: "Optional if you use OpenAI instead. Generate one at https://openrouter.ai/keys",
			})
		}
		if !ol.configHasOpenAIKey() {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "openai_api_key",
				Name:        "OpenAI API Key",
				Description: "Optional if you use OpenRouter instead. Generate one at https://platform.openai.com/account/api-keys",
			})
		}
		if !ol.configHasExaKey() {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "exa_api_key",
				Name:        "Exa API Key",
				Description: "Optional. Used for web search and fetch.",
			})
		}
	default:
		return nil
	}

	if len(fields) == 0 {
		return nil
	}

	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "com.beeper.agentremote.ai.enter_credentials",
		Instructions: "Enter your API credentials",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: fields,
		},
	}
}

func (ol *OpenAILogin) completeLogin(ctx context.Context, input loginCompletionInput) (*bridgev2.LoginStep, error) {
	provider := normalizeProvider(input.Provider)
	apiKey := strings.TrimSpace(input.APIKey)
	baseURL := stringutil.NormalizeBaseURL(input.BaseURL)
	serviceTokens := input.ServiceTokens
	if ol.User == nil {
		return nil, errAIMissingUserContext
	}

	override := ol.Override
	if override != nil {
		overrideMeta := loginMetadata(override)
		if overrideMeta == nil {
			return nil, errAIMissingReloginMeta
		}
		if !strings.EqualFold(normalizeProvider(overrideMeta.Provider), provider) {
			return nil, sdk.NewLoginRespError(http.StatusBadRequest, fmt.Sprintf("Can't relogin %s account with %s credentials.", overrideMeta.Provider, provider), "AI", "PROVIDER_MISMATCH")
		}
	}

	loginID, ordinal, err := ol.resolveLoginTarget(ctx, provider)
	if err != nil {
		return nil, err
	}

	remoteNameBase := formatRemoteName(provider, apiKey)
	remoteName := remoteNameBase
	if override != nil && strings.TrimSpace(override.RemoteName) != "" {
		remoteName = override.RemoteName
	} else if ordinal > 1 {
		remoteName = fmt.Sprintf("%s (%d)", remoteNameBase, ordinal)
	}

	meta := &UserLoginMetadata{}
	cfg := &aiLoginConfig{}
	if override != nil {
		meta, err = cloneUserLoginMetadata(loginMetadata(override))
		if err != nil {
			return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to clone relogin metadata: %w", err), http.StatusInternalServerError, "AI", "CLONE_RELOGIN_METADATA_FAILED")
		}
		cfg, err = loadAILoginConfig(ctx, override)
		if err != nil {
			return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to load relogin config: %w", err), http.StatusInternalServerError, "AI", "LOAD_RELOGIN_CONFIG_FAILED")
		}
	}
	if meta == nil {
		meta = &UserLoginMetadata{}
	}
	if cfg == nil {
		cfg = &aiLoginConfig{}
	}
	meta.Provider = provider
	creds := &LoginCredentials{
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
	if serviceTokens != nil && !serviceTokensEmpty(serviceTokens) {
		creds.ServiceTokens = cloneServiceTokens(serviceTokens)
	}
	if loginCredentialsEmpty(creds) {
		cfg.Credentials = nil
	} else {
		cfg.Credentials = creds
	}
	if err := ol.validateLoginMetadata(ctx, loginID, meta.Provider, cfg); err != nil {
		return nil, err
	}

	login, step, err := sdk.PersistAndCompleteLoginWithOptions(
		ctx,
		context.Background(),
		ol.User,
		&database.UserLogin{
			ID:         loginID,
			RemoteName: remoteName,
			Metadata:   meta,
		},
		"com.beeper.agentremote.ai.complete",
		sdk.PersistLoginCompletionOptions{
			NewLoginParams: &bridgev2.NewLoginParams{
				LoadUserLogin: func(loadCtx context.Context, login *bridgev2.UserLogin) error {
					if ol.Connector == nil {
						return nil
					}
					return ol.Connector.loadAIUserLoginWithConfig(loadCtx, login, meta, cfg)
				},
			},
			AfterPersist: func(saveCtx context.Context, login *bridgev2.UserLogin) error {
				return saveAILoginConfig(saveCtx, login, cfg)
			},
			Cleanup: func(cleanupCtx context.Context, login *bridgev2.UserLogin) {
				if login == nil {
					return
				}
				login.Delete(cleanupCtx, status.BridgeState{}, bridgev2.DeleteOpts{
					DontCleanupRooms: true,
					BlockingCleanup:  true,
				})
			},
		},
	)
	if err != nil {
		code := "CREATE_LOGIN_FAILED"
		if login != nil {
			code = "SAVE_LOGIN_FAILED"
		}
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to complete login: %w", err), http.StatusInternalServerError, "AI", code)
	}
	return step, nil
}

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
