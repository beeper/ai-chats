package connector

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// Provider constants - all use OpenAI SDK with different base URLs
const (
	ProviderBeeper     = "beeper"     // Beeper's OpenRouter proxy
	ProviderOpenAI     = "openai"     // Direct OpenAI API
	ProviderOpenRouter = "openrouter" // Direct OpenRouter API
	FlowCustom         = "custom"     // Custom login flow (provider resolved during login)
)

const beeperBasePath = "/_matrix/client/unstable/com.beeper.ai"

var beeperDomains = []string{
	"beeper.com",
	"beeper-dev.com",
	"beeper-staging.com",
	"beeper.localtest.me",
}

var (
	_ bridgev2.LoginProcess          = (*OpenAILogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*OpenAILogin)(nil)
)

// OpenAILogin maps a Matrix user to a synthetic OpenAI "login".
type OpenAILogin struct {
	User      *bridgev2.User
	Connector *OpenAIConnector
	FlowID    string
}

func (ol *OpenAILogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	step := ol.credentialsStep()
	if step != nil {
		return step, nil
	}

	switch ol.FlowID {
	case ProviderBeeper:
		baseURL := ol.Connector.resolveBeeperBaseURL(nil)
		apiKey := ol.Connector.resolveBeeperToken(nil)
		if baseURL == "" || apiKey == "" {
			return nil, &ErrBaseURLRequired
		}
		return ol.finishLogin(ctx, ProviderBeeper, apiKey, baseURL, nil)
	case FlowCustom:
		provider, apiKey, serviceTokens, err := ol.resolveCustomLogin(nil)
		if err != nil {
			return nil, err
		}
		return ol.finishLogin(ctx, provider, apiKey, "", serviceTokens)
	default:
		return nil, fmt.Errorf("login flow %s is not available", ol.FlowID)
	}
}

func (ol *OpenAILogin) Cancel() {}

func (ol *OpenAILogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	switch ol.FlowID {
	case ProviderBeeper:
		baseURL := strings.TrimRight(strings.TrimSpace(ol.Connector.Config.Beeper.BaseURL), "/")
		if baseURL == "" {
			domain := strings.TrimSpace(input["beeper_domain"])
			if domain == "" {
				return nil, &ErrBaseURLRequired
			}
			baseURL = beeperBaseURLFromDomain(domain)
		}
		baseURL = normalizeBeeperBaseURL(baseURL)
		apiKey := strings.TrimSpace(ol.Connector.Config.Beeper.Token)
		if apiKey == "" {
			apiKey = strings.TrimSpace(input["beeper_token"])
		}
		if apiKey == "" {
			return nil, &ErrAPIKeyRequired
		}
		return ol.finishLogin(ctx, ProviderBeeper, apiKey, baseURL, nil)
	case FlowCustom:
		provider, apiKey, serviceTokens, err := ol.resolveCustomLogin(input)
		if err != nil {
			return nil, err
		}
		return ol.finishLogin(ctx, provider, apiKey, "", serviceTokens)
	default:
		return nil, fmt.Errorf("login flow %s is not available", ol.FlowID)
	}
}

func (ol *OpenAILogin) credentialsStep() *bridgev2.LoginStep {
	var fields []bridgev2.LoginInputDataField
	switch ol.FlowID {
	case ProviderBeeper:
		if strings.TrimSpace(ol.Connector.Config.Beeper.BaseURL) == "" {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:         bridgev2.LoginInputFieldTypeSelect,
				ID:           "beeper_domain",
				Name:         "Beeper",
				Description:  fmt.Sprintf("Select your Beeper domain (%s).", strings.Join(beeperDomains, ", ")),
				DefaultValue: "beeper.com",
				Options:      beeperDomains,
			})
		}
		if strings.TrimSpace(ol.Connector.Config.Beeper.Token) == "" {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "beeper_token",
				Name:        "Beeper AI key",
				Description: "Beeper AI needs a key to connect to Beeper servers. Requires Beeper Plus.",
			})
		}
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
		if !ol.configHasBraveKey() {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "brave_api_key",
				Name:        "Brave Search API Key",
				Description: "Optional. Used for web search.",
			})
		}
		if !ol.configHasPerplexityKey() {
			fields = append(fields, bridgev2.LoginInputDataField{
				Type:        bridgev2.LoginInputFieldTypeToken,
				ID:          "perplexity_api_key",
				Name:        "Perplexity API Key",
				Description: "Optional. Used for web search via OpenRouter.",
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
		StepID:       "io.ai-bridge.openai.enter_credentials",
		Instructions: "Enter your API credentials",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: fields,
		},
	}
}

func (ol *OpenAILogin) finishLogin(ctx context.Context, provider, apiKey, baseURL string, serviceTokens *ServiceTokens) (*bridgev2.LoginStep, error) {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	loginID := makeUserLoginID(ol.User.MXID, provider, apiKey)
	meta := &UserLoginMetadata{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
	}
	if serviceTokens != nil && !serviceTokensEmpty(serviceTokens) {
		meta.ServiceTokens = serviceTokens
	}
	login, err := ol.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: formatRemoteName(provider, apiKey),
		Metadata:   meta,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create login: %w", err)
	}

	// Load login (which validates and caches the client internally)
	err = ol.Connector.LoadUserLogin(ctx, login)
	if err != nil {
		return nil, fmt.Errorf("failed to load client: %w", err)
	}

	// Trigger connection in background with a long-lived context
	// (the request context gets cancelled after login returns)
	go login.Client.Connect(login.Log.WithContext(context.Background()))

	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeComplete,
		StepID: "io.ai-bridge.openai.complete",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}, nil
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

	provider := ProviderOpenAI
	apiKey := openaiToken
	if openrouterToken != "" {
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
	if !ol.configHasBraveKey() {
		serviceTokens.Brave = strings.TrimSpace(input["brave_api_key"])
	}
	if !ol.configHasPerplexityKey() {
		serviceTokens.Perplexity = strings.TrimSpace(input["perplexity_api_key"])
	}

	return provider, apiKey, serviceTokens, nil
}

func serviceTokensEmpty(tokens *ServiceTokens) bool {
	if tokens == nil {
		return true
	}
	return strings.TrimSpace(tokens.OpenAI) == "" &&
		strings.TrimSpace(tokens.OpenRouter) == "" &&
		strings.TrimSpace(tokens.Exa) == "" &&
		strings.TrimSpace(tokens.Brave) == "" &&
		strings.TrimSpace(tokens.Perplexity) == ""
}

func beeperBaseURLFromDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	if !strings.HasPrefix(domain, "matrix.") {
		domain = "matrix." + domain
	}
	return "https://" + domain + beeperBasePath
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

func (ol *OpenAILogin) configHasBraveKey() bool {
	if ol.Connector.Config.Tools.Search == nil {
		return false
	}
	return strings.TrimSpace(ol.Connector.Config.Tools.Search.Brave.APIKey) != ""
}

func (ol *OpenAILogin) configHasPerplexityKey() bool {
	if ol.Connector.Config.Tools.Search == nil {
		return false
	}
	return strings.TrimSpace(ol.Connector.Config.Tools.Search.Perplexity.APIKey) != ""
}

// formatRemoteName generates a display name for the account based on provider.
func formatRemoteName(provider, apiKey string) string {
	switch provider {
	case ProviderBeeper:
		return "Beeper AI"
	case ProviderOpenAI:
		return fmt.Sprintf("OpenAI (%s)", maskAPIKey(apiKey))
	case ProviderOpenRouter:
		return fmt.Sprintf("OpenRouter (%s)", maskAPIKey(apiKey))
	default:
		return "AI Bridge"
	}
}

// maskAPIKey returns a masked version of the API key showing first 3 and last 3 chars.
func maskAPIKey(key string) string {
	if len(key) <= 6 {
		return "***"
	}
	return fmt.Sprintf("%s...%s", key[:3], key[len(key)-3:])
}
