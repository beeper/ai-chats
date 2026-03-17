package openclaw

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

var (
	_ bridgev2.LoginProcess               = (*OpenClawLogin)(nil)
	_ bridgev2.LoginProcessUserInput      = (*OpenClawLogin)(nil)
	_ bridgev2.LoginProcessDisplayAndWait = (*OpenClawLogin)(nil)
)

const openClawLoginStepCredentials = "io.ai-bridge.openclaw.enter_credentials"

const (
	openClawLoginStepAuthMode          = "io.ai-bridge.openclaw.choose_auth_mode"
	openClawLoginStepCredentialsNoAuth = "io.ai-bridge.openclaw.enter_credentials.none"
	openClawLoginStepCredentialsToken  = "io.ai-bridge.openclaw.enter_credentials.token"
	openClawLoginStepCredentialsPass   = "io.ai-bridge.openclaw.enter_credentials.password"
	openClawLoginStepPairingWait       = "io.ai-bridge.openclaw.wait_for_pairing"
)

type openClawLoginState string

const (
	openClawLoginStateAuthMode    openClawLoginState = "auth_mode"
	openClawLoginStateCredentials openClawLoginState = "credentials"
	openClawLoginStatePairingWait openClawLoginState = "pairing_wait"
)

const (
	openClawPairingPollInterval = 2 * time.Second
	openClawPairingReturnAfter  = 20 * time.Second
	openClawPairingWaitTimeout  = 10 * time.Minute
	openClawPreflightTimeout    = 20 * time.Second
	openClawPreflightConnect    = 10 * time.Second
	openClawPreflightList       = 10 * time.Second
)

type openClawPendingLogin struct {
	gatewayURL string
	authMode   string
	token      string
	password   string
	label      string
	requestID  string
}

type OpenClawLogin struct {
	agentremote.BaseLoginProcess
	User      *bridgev2.User
	Connector *OpenClawConnector

	step       openClawLoginState
	authMode   string
	pending    *openClawPendingLogin
	waitUntil  time.Time
	preflight  func(context.Context, string, string, string) (string, error)
	pollEvery  time.Duration
	returnWait time.Duration
	waitFor    time.Duration
}

func (ol *OpenClawLogin) validate() error {
	var br *bridgev2.Bridge
	if ol.Connector != nil {
		br = ol.Connector.br
	}
	return agentremote.ValidateLoginState(ol.User, br)
}

func (ol *OpenClawLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	ol.step = openClawLoginStateAuthMode
	ol.authMode = ""
	ol.pending = nil
	ol.waitUntil = time.Time{}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       openClawLoginStepAuthMode,
		Instructions: "Choose how the bridge should authenticate to your OpenClaw gateway.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:        bridgev2.LoginInputFieldTypeSelect,
					ID:          "auth_mode",
					Name:        "Authentication Mode",
					Description: "Pick the gateway auth mode first so the next step only asks for the fields that matter.",
					Options:     []string{"No auth", "Token", "Password"},
				},
			},
		},
	}, nil
}

func (ol *OpenClawLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	switch ol.step {
	case "", openClawLoginStateAuthMode:
		authMode, err := normalizeOpenClawAuthMode(input["auth_mode"])
		if err != nil {
			return nil, err
		}
		ol.step = openClawLoginStateCredentials
		ol.authMode = authMode
		return openClawCredentialStep(authMode), nil
	case openClawLoginStateCredentials:
	default:
		return nil, errors.New("login process is in an invalid state")
	}

	authMode, err := normalizeOpenClawAuthMode(ol.authMode)
	if err != nil {
		return nil, err
	}
	normalizedURL, err := normalizeOpenClawLoginURL(input["url"])
	if err != nil {
		return nil, err
	}
	token, password, err := normalizeOpenClawAuthCredentials(authMode, input)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(input["label"])
	pending := &openClawPendingLogin{
		gatewayURL: normalizedURL,
		authMode:   authMode,
		token:      token,
		password:   password,
		label:      label,
	}
	deviceToken, err := ol.preflightGatewayLogin(ctx, pending.gatewayURL, pending.token, pending.password)
	if err != nil {
		var rpcErr *gatewayRPCError
		if errors.As(err, &rpcErr) && rpcErr.IsPairingRequired() {
			pending.requestID = strings.TrimSpace(rpcErr.RequestID)
			ol.pending = pending
			ol.step = openClawLoginStatePairingWait
			ol.waitUntil = time.Now().Add(ol.waitDuration())
			return openClawPairingWaitStep(pending.requestID, false), nil
		}
		return nil, mapOpenClawLoginError(err)
	}
	return ol.completeLogin(pending, deviceToken)
}

func (ol *OpenClawLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	if ol.step != openClawLoginStatePairingWait || ol.pending == nil {
		return nil, errors.New("login is not waiting for OpenClaw pairing")
	}
	if ol.waitUntil.IsZero() {
		ol.waitUntil = time.Now().Add(ol.waitDuration())
	}
	remaining := time.Until(ol.waitUntil)
	if remaining <= 0 {
		ol.Cancel()
		return nil, errors.New("timed out waiting for OpenClaw pairing approval")
	}

	deadline := time.NewTimer(remaining)
	defer deadline.Stop()
	tick := time.NewTicker(ol.pollInterval())
	defer tick.Stop()
	returnAfter := time.NewTimer(ol.waitReturnAfter())
	defer returnAfter.Stop()

	for {
		select {
		case <-tick.C:
			deviceToken, err := ol.preflightGatewayLogin(ol.BackgroundProcessContext(), ol.pending.gatewayURL, ol.pending.token, ol.pending.password)
			if err == nil {
				return ol.completeLogin(ol.pending, deviceToken)
			}
			var rpcErr *gatewayRPCError
			if errors.As(err, &rpcErr) && rpcErr.IsPairingRequired() {
				if requestID := strings.TrimSpace(rpcErr.RequestID); requestID != "" {
					ol.pending.requestID = requestID
				}
				continue
			}
			ol.Cancel()
			return nil, mapOpenClawLoginError(err)
		case <-returnAfter.C:
			return openClawPairingWaitStep(ol.pending.requestID, true), nil
		case <-deadline.C:
			ol.Cancel()
			return nil, errors.New("timed out waiting for OpenClaw pairing approval")
		case <-ctx.Done():
			return openClawPairingWaitStep(ol.pending.requestID, true), nil
		}
	}
}

func (ol *OpenClawLogin) Cancel() {
	ol.BaseLoginProcess.Cancel()
	ol.pending = nil
	ol.waitUntil = time.Time{}
}

func (ol *OpenClawLogin) pollInterval() time.Duration {
	if ol.pollEvery > 0 {
		return ol.pollEvery
	}
	return openClawPairingPollInterval
}

func (ol *OpenClawLogin) waitReturnAfter() time.Duration {
	if ol.returnWait > 0 {
		return ol.returnWait
	}
	return openClawPairingReturnAfter
}

func (ol *OpenClawLogin) waitDuration() time.Duration {
	if ol.waitFor > 0 {
		return ol.waitFor
	}
	return openClawPairingWaitTimeout
}

func openClawPairingWaitStep(requestID string, stillWaiting bool) *bridgev2.LoginStep {
	instructions := "Approve the pending OpenClaw device pairing request, then keep this screen open while the bridge reconnects."
	if stillWaiting {
		instructions = "Still waiting for OpenClaw device pairing approval. Keep this screen open while the bridge retries."
	}
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		instructions += fmt.Sprintf(" Request ID: %s.", requestID)
		instructions += fmt.Sprintf(" Approve it with `openclaw devices approve %s`.", requestID)
	} else {
		instructions += " Find the pending request with `openclaw devices list` and approve it with `openclaw devices approve <request-id>`."
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeDisplayAndWait,
		StepID:       openClawLoginStepPairingWait,
		Instructions: instructions,
		DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{
			Type: bridgev2.LoginDisplayTypeNothing,
		},
	}
}

func (ol *OpenClawLogin) completeLogin(pending *openClawPendingLogin, deviceToken string) (*bridgev2.LoginStep, error) {
	if pending == nil {
		return nil, errors.New("missing pending OpenClaw login details")
	}
	persistCtx := ol.BackgroundProcessContext()
	log := ol.User.Log.With().Str("component", "openclaw_login").Str("gateway_url", pending.gatewayURL).Logger()
	remoteName := openClawRemoteName(pending.gatewayURL, pending.label)
	loginID := agentremote.NextUserLoginID(ol.User, "openclaw")
	log.Debug().Str("login_id", string(loginID)).Str("remote_name", remoteName).Msg("Creating OpenClaw user login")
	login, step, err := agentremote.CreateAndCompleteLogin(
		persistCtx,
		ol.BackgroundProcessContext(),
		ol.User,
		"openclaw",
		remoteName,
		&UserLoginMetadata{
			Provider:        ProviderOpenClaw,
			GatewayURL:      pending.gatewayURL,
			AuthMode:        pending.authMode,
			GatewayToken:    pending.token,
			GatewayPassword: pending.password,
			GatewayLabel:    pending.label,
			DeviceToken:     deviceToken,
		},
		"io.ai-bridge.openclaw.complete",
		nil,
	)
	if err != nil {
		log.Debug().Err(err).Str("login_id", string(loginID)).Msg("OpenClaw user login creation failed")
		return nil, fmt.Errorf("failed to create login: %w", err)
	}
	log.Debug().Str("login_id", string(login.ID)).Msg("Created OpenClaw user login")
	ol.pending = nil
	ol.step = ""
	ol.waitUntil = time.Time{}
	return step, nil
}

func openClawCredentialStep(authMode string) *bridgev2.LoginStep {
	fields := []bridgev2.LoginInputDataField{
		{
			Type:         bridgev2.LoginInputFieldTypeURL,
			ID:           "url",
			Name:         "Gateway URL",
			Description:  "OpenClaw gateway URL, e.g. ws://localhost:18789 or https://gateway.example.com",
			DefaultValue: "ws://127.0.0.1:18789",
		},
	}
	stepID := openClawLoginStepCredentials
	instructions := "Enter your OpenClaw gateway details."
	switch authMode {
	case "token":
		stepID = openClawLoginStepCredentialsToken
		instructions = "Enter the OpenClaw gateway URL and shared token."
		fields = append(fields, bridgev2.LoginInputDataField{
			Type:        bridgev2.LoginInputFieldTypeToken,
			ID:          "token",
			Name:        "Gateway Token",
			Description: "Shared gateway token or operator device token.",
		})
	case "password":
		stepID = openClawLoginStepCredentialsPass
		instructions = "Enter the OpenClaw gateway URL and shared password."
		fields = append(fields, bridgev2.LoginInputDataField{
			Type:        bridgev2.LoginInputFieldTypePassword,
			ID:          "password",
			Name:        "Gateway Password",
			Description: "Shared password for the gateway.",
		})
	default:
		stepID = openClawLoginStepCredentialsNoAuth
		instructions = "Enter the OpenClaw gateway URL."
	}
	fields = append(fields, bridgev2.LoginInputDataField{
		Type:        bridgev2.LoginInputFieldTypeUsername,
		ID:          "label",
		Name:        "Gateway Label",
		Description: "Optional label to distinguish multiple gateways.",
	})
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       stepID,
		Instructions: instructions,
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: fields,
		},
	}
}

func normalizeOpenClawAuthMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "no auth":
		return "none", nil
	case "token":
		return "token", nil
	case "password":
		return "password", nil
	default:
		return "", fmt.Errorf("unsupported auth mode %q", raw)
	}
}

func normalizeOpenClawAuthCredentials(authMode string, input map[string]string) (string, string, error) {
	token := strings.TrimSpace(input["token"])
	password := strings.TrimSpace(input["password"])
	switch authMode {
	case "none":
		return "", "", nil
	case "token":
		if token == "" {
			return "", "", errors.New("gateway token is required")
		}
		return token, "", nil
	case "password":
		if password == "" {
			return "", "", errors.New("gateway password is required")
		}
		return "", password, nil
	default:
		return "", "", fmt.Errorf("unsupported auth mode %q", authMode)
	}
}

func (ol *OpenClawLogin) preflightGatewayLogin(ctx context.Context, gatewayURL, token, password string) (string, error) {
	if ol.preflight != nil {
		return ol.preflight(ctx, gatewayURL, token, password)
	}
	log := ol.User.Log.With().Str("component", "openclaw_login").Logger()
	ctx, cancel := openClawBoundedContext(ctx, openClawPreflightTimeout)
	defer cancel()
	log.Debug().Str("gateway_url", gatewayURL).Msg("Starting OpenClaw gateway preflight")

	client := newGatewayWSClient(gatewayConnectConfig{
		URL:      gatewayURL,
		Token:    token,
		Password: password,
	})

	connectCtx, connectCancel := openClawBoundedContext(ctx, openClawPreflightConnect)
	deviceToken, err := client.Connect(connectCtx)
	connectCancel()
	if err != nil {
		log.Debug().Err(err).Str("gateway_url", gatewayURL).Msg("OpenClaw gateway preflight connect failed")
		return "", err
	}
	defer client.CloseNow()

	listCtx, listCancel := openClawBoundedContext(ctx, openClawPreflightList)
	_, err = client.ListSessions(listCtx, 1)
	listCancel()
	if err != nil {
		log.Debug().Err(err).Str("gateway_url", gatewayURL).Msg("OpenClaw gateway preflight sessions.list failed")
		return "", err
	}
	log.Debug().Str("gateway_url", gatewayURL).Msg("Completed OpenClaw gateway preflight")
	return deviceToken, nil
}

func openClawBoundedContext(ctx context.Context, max time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= max {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, max)
}

func mapOpenClawLoginError(err error) error {
	var rpcErr *gatewayRPCError
	if !errors.As(err, &rpcErr) {
		return err
	}
	switch {
	case rpcErr.IsPairingRequired():
		msg := "OpenClaw device pairing is required."
		if requestID := strings.TrimSpace(rpcErr.RequestID); requestID != "" {
			msg += fmt.Sprintf(" Approve request %s with `openclaw devices approve %s`", requestID, requestID)
		} else {
			msg += " Approve the pending device with `openclaw devices list` and `openclaw devices approve <request-id>`"
		}
		msg += ", then try logging in again."
		return bridgev2.WrapRespErr(errors.New(msg), mautrix.MForbidden)
	case strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rpcErr.DetailCode)), "AUTH_"):
		return bridgev2.WrapRespErr(errors.New(rpcErr.Error()), mautrix.MForbidden)
	default:
		return rpcErr
	}
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
