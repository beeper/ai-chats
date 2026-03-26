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

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
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

	errAIReloginTargetInvalid = agentremote.NewLoginRespError(http.StatusBadRequest, "Invalid relogin target.", "AI", "INVALID_RELOGIN_TARGET")
	errAIMissingUserContext   = agentremote.NewLoginRespError(http.StatusInternalServerError, "Missing user context for login.", "AI", "MISSING_USER_CONTEXT")
	errAIMissingReloginMeta   = agentremote.NewLoginRespError(http.StatusInternalServerError, "Missing relogin metadata.", "AI", "MISSING_RELOGIN_METADATA")
)

// OpenAILogin maps a Matrix user to a synthetic OpenAI "login".
type OpenAILogin struct {
	User      *bridgev2.User
	Connector *OpenAIConnector
	FlowID    string
	Override  *bridgev2.UserLogin
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
	step := ol.credentialsStep()
	if step != nil {
		return step, nil
	}

	switch ol.FlowID {
	case ProviderMagicProxy:
		return nil, &ErrBaseURLRequired
	case FlowCustom:
		provider, apiKey, serviceTokens, err := ol.resolveCustomLogin(nil)
		if err != nil {
			return nil, err
		}
		return ol.finishLogin(ctx, provider, apiKey, "", serviceTokens)
	default:
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
}

func (ol *OpenAILogin) Cancel() {}

func (ol *OpenAILogin) StartWithOverride(ctx context.Context, old *bridgev2.UserLogin) (*bridgev2.LoginStep, error) {
	if old == nil {
		return ol.Start(ctx)
	}
	if ol.User == nil || old.UserMXID != ol.User.MXID {
		return nil, errAIReloginTargetInvalid
	}
	ol.Override = old
	return ol.Start(ctx)
}

func (ol *OpenAILogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	switch ol.FlowID {
	case ProviderMagicProxy:
		link := strings.TrimSpace(input["magic_proxy_link"])
		baseURL, apiKey, err := parseMagicProxyLink(link)
		if err != nil {
			return nil, err
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
		return ol.finishLogin(ctx, ProviderMagicProxy, apiKey, baseURL, nil)
	case FlowCustom:
		provider, apiKey, serviceTokens, err := ol.resolveCustomLogin(input)
		if err != nil {
			return nil, err
		}
		return ol.finishLogin(ctx, provider, apiKey, "", serviceTokens)
	default:
		return nil, bridgev2.ErrInvalidLoginFlowID
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
		StepID:       "com.beeper.agentremote.openai.enter_credentials",
		Instructions: "Enter your API credentials",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: fields,
		},
	}
}

func (ol *OpenAILogin) finishLogin(ctx context.Context, provider, apiKey, baseURL string, serviceTokens *ServiceTokens) (*bridgev2.LoginStep, error) {
	provider = normalizeProvider(provider)
	apiKey = strings.TrimSpace(apiKey)
	baseURL = stringutil.NormalizeBaseURL(baseURL)
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
			return nil, agentremote.NewLoginRespError(http.StatusBadRequest, fmt.Sprintf("Can't relogin %s account with %s credentials.", overrideMeta.Provider, provider), "AI", "PROVIDER_MISMATCH")
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
	if override != nil {
		meta, err = cloneUserLoginMetadata(loginMetadata(override))
		if err != nil {
			return nil, agentremote.WrapLoginRespError(fmt.Errorf("failed to clone relogin metadata: %w", err), http.StatusInternalServerError, "AI", "CLONE_RELOGIN_METADATA_FAILED")
		}
	}
	if meta == nil {
		meta = &UserLoginMetadata{}
	}
	meta.Provider = provider
	meta.APIKey = apiKey
	meta.BaseURL = baseURL
	if serviceTokens != nil && !serviceTokensEmpty(serviceTokens) {
		meta.ServiceTokens = mergeServiceTokens(meta.ServiceTokens, serviceTokens)
	}
	if err := ol.validateLoginMetadata(ctx, loginID, meta); err != nil {
		return nil, err
	}

	login, err := ol.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: remoteName,
		Metadata:   meta,
	}, nil)
	if err != nil {
		return nil, agentremote.WrapLoginRespError(fmt.Errorf("failed to create login: %w", err), http.StatusInternalServerError, "AI", "CREATE_LOGIN_FAILED")
	}

	// Trigger connection in background with a long-lived context
	// (the request context gets cancelled after login returns)
	go login.Client.Connect(login.Log.WithContext(context.Background()))

	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeComplete,
		StepID: "com.beeper.agentremote.openai.complete",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}, nil
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

func (ol *OpenAILogin) validateLoginMetadata(ctx context.Context, loginID networkid.UserLoginID, meta *UserLoginMetadata) error {
	if ol == nil || ol.User == nil || ol.Connector == nil || meta == nil {
		return nil
	}
	tempDBLogin := &database.UserLogin{
		ID:       loginID,
		UserMXID: ol.User.MXID,
		Metadata: meta,
	}
	tempLogin := &bridgev2.UserLogin{
		UserLogin: tempDBLogin,
		Bridge:    ol.User.Bridge,
		User:      ol.User,
		Log:       ol.User.Log.With().Str("login_id", string(loginID)).Str("component", "ai-login-validation").Logger(),
	}
	tempClient, err := newAIClient(tempLogin, ol.Connector, ol.Connector.resolveProviderAPIKey(meta))
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

func serviceTokensEmpty(tokens *ServiceTokens) bool {
	if tokens == nil {
		return true
	}
	if len(tokens.DesktopAPIInstances) > 0 {
		for _, instance := range tokens.DesktopAPIInstances {
			if strings.TrimSpace(instance.Token) != "" || strings.TrimSpace(instance.BaseURL) != "" {
				return false
			}
		}
	}
	if len(tokens.MCPServers) > 0 {
		for _, server := range tokens.MCPServers {
			if strings.TrimSpace(server.Transport) != "" ||
				strings.TrimSpace(server.Endpoint) != "" ||
				strings.TrimSpace(server.Command) != "" ||
				len(server.Args) > 0 ||
				strings.TrimSpace(server.Token) != "" ||
				strings.TrimSpace(server.AuthURL) != "" ||
				strings.TrimSpace(server.AuthType) != "" ||
				strings.TrimSpace(server.Kind) != "" ||
				server.Connected {
				return false
			}
		}
	}
	return strings.TrimSpace(tokens.OpenAI) == "" &&
		strings.TrimSpace(tokens.OpenRouter) == "" &&
		strings.TrimSpace(tokens.Exa) == "" &&
		strings.TrimSpace(tokens.Brave) == "" &&
		strings.TrimSpace(tokens.Perplexity) == "" &&
		strings.TrimSpace(tokens.DesktopAPI) == ""
}

func (ol *OpenAILogin) resolveCustomLogin(input map[string]string) (string, string, *ServiceTokens, error) {
	if input == nil {
		input = map[string]string{}
	}
	openrouterCfg := strings.TrimSpace(ol.Connector.Config.Providers.OpenRouter.APIKey)
	openaiCfg := strings.TrimSpace(ol.Connector.Config.Providers.OpenAI.APIKey)

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
	return strings.TrimSpace(ol.Connector.Config.Providers.OpenRouter.APIKey) != ""
}

func (ol *OpenAILogin) configHasOpenAIKey() bool {
	return strings.TrimSpace(ol.Connector.Config.Providers.OpenAI.APIKey) != ""
}

func (ol *OpenAILogin) configHasExaKey() bool {
	if ol.Connector.Config.Tools.Search != nil && strings.TrimSpace(ol.Connector.Config.Tools.Search.Exa.APIKey) != "" {
		return true
	}
	if ol.Connector.Config.Tools.Fetch != nil && strings.TrimSpace(ol.Connector.Config.Tools.Fetch.Exa.APIKey) != "" {
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
		return "AI Bridge"
	}
}

func maskAPIKey(key string) string {
	if len(key) <= 6 {
		return "***"
	}
	return fmt.Sprintf("%s...%s", key[:3], key[len(key)-3:])
}
