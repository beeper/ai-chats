package openclaw

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

var (
	_ bridgev2.LoginProcess          = (*OpenClawLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*OpenClawLogin)(nil)
)

const openClawLoginStepCredentials = "io.ai-bridge.openclaw.enter_credentials"

type OpenClawLogin struct {
	User      *bridgev2.User
	Connector *OpenClawConnector

	bgMu     sync.Mutex
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

func (ol *OpenClawLogin) validate() error {
	if ol.User == nil {
		return errors.New("missing user context for login")
	}
	if ol.Connector == nil || ol.Connector.br == nil {
		return errors.New("connector is not initialized")
	}
	return nil
}

func (ol *OpenClawLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       openClawLoginStepCredentials,
		Instructions: "Enter your OpenClaw gateway details.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:         bridgev2.LoginInputFieldTypeURL,
					ID:           "url",
					Name:         "Gateway URL",
					Description:  "OpenClaw gateway URL, e.g. ws://localhost:18789 or https://gateway.example.com",
					DefaultValue: "ws://127.0.0.1:18789",
				},
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "token",
					Name:        "Gateway Token",
					Description: "Optional shared token or operator device token.",
				},
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "password",
					Name:        "Gateway Password",
					Description: "Optional shared password. Used when no token is provided.",
				},
				{
					Type:        bridgev2.LoginInputFieldTypeUsername,
					ID:          "label",
					Name:        "Gateway Label",
					Description: "Optional label to distinguish multiple gateways.",
				},
			},
		},
	}, nil
}

func (ol *OpenClawLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	normalizedURL, err := normalizeOpenClawLoginURL(input["url"])
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(input["token"])
	password := strings.TrimSpace(input["password"])
	label := strings.TrimSpace(input["label"])
	authMode := "none"
	if token != "" {
		authMode = "token"
		password = ""
	} else if password != "" {
		authMode = "password"
		token = ""
	}

	remoteName := openClawRemoteName(normalizedURL, label)
	loginID := nextOpenClawUserLoginID(ol.User)
	login, err := ol.User.NewLogin(ctx, &database.UserLogin{
		ID:         loginID,
		RemoteName: remoteName,
		Metadata: &UserLoginMetadata{
			Provider:        ProviderOpenClaw,
			GatewayURL:      normalizedURL,
			AuthMode:        authMode,
			GatewayToken:    token,
			GatewayPassword: password,
			GatewayLabel:    label,
		},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create login: %w", err)
	}
	if err = ol.Connector.LoadUserLogin(ctx, login); err != nil {
		return nil, fmt.Errorf("failed to load client: %w", err)
	}
	if login.Client != nil {
		go login.Client.Connect(login.Log.WithContext(ol.backgroundProcessContext()))
	}
	return &bridgev2.LoginStep{
		Type:   bridgev2.LoginStepTypeComplete,
		StepID: "io.ai-bridge.openclaw.complete",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}, nil
}

func (ol *OpenClawLogin) Cancel() {
	ol.bgMu.Lock()
	cancel := ol.bgCancel
	ol.bgCancel = nil
	ol.bgCtx = nil
	ol.bgMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (ol *OpenClawLogin) backgroundProcessContext() context.Context {
	ol.bgMu.Lock()
	defer ol.bgMu.Unlock()
	if ol.bgCtx == nil || ol.bgCancel == nil {
		ol.bgCtx, ol.bgCancel = context.WithCancel(context.Background())
	}
	return ol.bgCtx
}

func normalizeOpenClawLoginURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "ws"
	}
	if parsed.Host == "" {
		return "", errors.New("gateway url host is required")
	}
	return parsed.String(), nil
}

func openClawRemoteName(gatewayURL, label string) string {
	parsed, err := url.Parse(gatewayURL)
	if err != nil || parsed.Host == "" {
		if label != "" {
			return "OpenClaw (" + label + ")"
		}
		return "OpenClaw"
	}
	if label == "" {
		return "OpenClaw (" + parsed.Host + ")"
	}
	return fmt.Sprintf("OpenClaw (%s - %s)", label, parsed.Host)
}
