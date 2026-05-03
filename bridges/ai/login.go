package ai

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-chats/sdk"
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
		StepID:       "com.beeper.ai_chats.ai.enter_credentials",
		Instructions: "Enter your API credentials",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: fields,
		},
	}
}
