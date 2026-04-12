package openclaw

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.LoginProcess               = (*OpenClawLogin)(nil)
	_ bridgev2.LoginProcessUserInput      = (*OpenClawLogin)(nil)
	_ bridgev2.LoginProcessDisplayAndWait = (*OpenClawLogin)(nil)
)

const (
	openClawLoginStepCredentials = "com.beeper.agentremote.openclaw.enter_credentials"
	openClawLoginStepPairingWait = "com.beeper.agentremote.openclaw.wait_for_pairing"
)

type openClawLoginState string

const (
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

var (
	errOpenClawInvalidState = sdk.NewLoginRespError(http.StatusBadRequest, "Login process is in an invalid state.", "OPENCLAW", "INVALID_STATE")
	errOpenClawNotWaiting   = sdk.NewLoginRespError(http.StatusBadRequest, "Login is not waiting for OpenClaw pairing.", "OPENCLAW", "NOT_WAITING")
	errOpenClawTimedOut     = sdk.NewLoginRespError(http.StatusBadRequest, "Timed out waiting for OpenClaw pairing approval.", "OPENCLAW", "PAIRING_TIMEOUT")
	errOpenClawMissingLogin = sdk.NewLoginRespError(http.StatusInternalServerError, "Missing pending OpenClaw login details.", "OPENCLAW", "MISSING_PENDING_LOGIN")
	errOpenClawMixedAuth    = sdk.NewLoginRespError(http.StatusBadRequest, "Provide either a gateway token or a gateway password, not both.", "OPENCLAW", "MIXED_AUTH")
	errOpenClawMissingHost  = sdk.NewLoginRespError(http.StatusBadRequest, "Gateway URL host is required.", "OPENCLAW", "MISSING_HOST")
)

type openClawPendingLogin struct {
	gatewayURL string
	token      string
	password   string
	label      string
	requestID  string
}

type OpenClawLogin struct {
	sdk.BaseLoginProcess
	User      *bridgev2.User
	Connector *OpenClawConnector

	step         openClawLoginState
	pending      *openClawPendingLogin
	waitUntil    time.Time
	prefillURL   string
	prefillLabel string
	preflight    func(context.Context, string, string, string) (string, error)
	pollEvery    time.Duration
	returnWait   time.Duration
	waitFor      time.Duration
}

func (ol *OpenClawLogin) validate() error {
	var br *bridgev2.Bridge
	if ol.Connector != nil {
		br = ol.Connector.br
	}
	return sdk.ValidateLoginState(ol.User, br)
}

func (ol *OpenClawLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	ol.step = openClawLoginStateCredentials
	ol.pending = nil
	ol.waitUntil = time.Time{}
	return openClawCredentialStep(ol.prefillURL, ol.prefillLabel), nil
}

func (ol *OpenClawLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if err := ol.validate(); err != nil {
		return nil, err
	}
	switch ol.step {
	case "", openClawLoginStateCredentials:
	default:
		return nil, errOpenClawInvalidState
	}

	normalizedURL, err := normalizeOpenClawLoginURL(input["url"])
	if err != nil {
		return nil, err
	}
	token, password, err := normalizeOpenClawAuthCredentials(input)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSpace(input["label"])
	pending := &openClawPendingLogin{
		gatewayURL: normalizedURL,
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
		return nil, errOpenClawNotWaiting
	}
	if ol.waitUntil.IsZero() {
		ol.waitUntil = time.Now().Add(ol.waitDuration())
	}
	remaining := time.Until(ol.waitUntil)
	if remaining <= 0 {
		ol.Cancel()
		return nil, errOpenClawTimedOut
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
			return nil, errOpenClawTimedOut
		case <-ctx.Done():
			return openClawPairingWaitStep(ol.pending.requestID, true), nil
		}
	}
}

func (ol *OpenClawLogin) Cancel() {
	ol.BaseLoginProcess.Cancel()
	ol.step = ""
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
		return nil, errOpenClawMissingLogin
	}
	persistCtx := ol.BackgroundProcessContext()
	log := ol.User.Log.With().Str("component", "openclaw_login").Str("gateway_url", pending.gatewayURL).Logger()
	remoteName := openClawRemoteName(pending.gatewayURL, pending.label)
	loginID := sdk.NextUserLoginID(ol.User, "openclaw")
	log.Debug().Str("login_id", string(loginID)).Str("remote_name", remoteName).Msg("Creating OpenClaw user login")
	login, step, err := sdk.CreateAndCompleteLogin(
		persistCtx,
		ol.BackgroundProcessContext(),
		ol.User,
		"openclaw",
		remoteName,
		&UserLoginMetadata{
			Provider:     ProviderOpenClaw,
			GatewayURL:   pending.gatewayURL,
			GatewayLabel: pending.label,
		},
		"com.beeper.agentremote.openclaw.complete",
		nil,
	)
	if err != nil {
		log.Debug().Err(err).Str("login_id", string(loginID)).Msg("OpenClaw user login creation failed")
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to create login: %w", err), http.StatusInternalServerError, "OPENCLAW", "CREATE_LOGIN_FAILED")
	}
	log.Debug().Str("login_id", string(login.ID)).Msg("Created OpenClaw user login")
	if err := saveOpenClawLoginState(persistCtx, login, &openClawPersistedLoginState{
		GatewayToken:    pending.token,
		GatewayPassword: pending.password,
		DeviceToken:     deviceToken,
	}); err != nil {
		log.Warn().Err(err).Str("login_id", string(login.ID)).Msg("Failed to persist OpenClaw login state")
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to persist login state: %w", err), http.StatusInternalServerError, "OPENCLAW", "SAVE_LOGIN_STATE_FAILED")
	}
	ol.pending = nil
	ol.step = ""
	ol.waitUntil = time.Time{}
	return step, nil
}

func openClawCredentialStep(defaultURL, defaultLabel string) *bridgev2.LoginStep {
	defaultURL = strings.TrimSpace(defaultURL)
	if defaultURL == "" {
		defaultURL = "ws://127.0.0.1:18789"
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       openClawLoginStepCredentials,
		Instructions: "Enter your OpenClaw gateway details. Leave token and password empty for no auth, or provide exactly one of them.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{
				{
					Type:         bridgev2.LoginInputFieldTypeURL,
					ID:           "url",
					Name:         "Gateway URL",
					Description:  "OpenClaw gateway URL, e.g. ws://localhost:18789 or https://gateway.example.com",
					DefaultValue: defaultURL,
				},
				{
					Type:        bridgev2.LoginInputFieldTypeToken,
					ID:          "token",
					Name:        "Gateway Token",
					Description: "Optional shared gateway token or operator device token. Do not fill both token and password.",
				},
				{
					Type:        bridgev2.LoginInputFieldTypePassword,
					ID:          "password",
					Name:        "Gateway Password",
					Description: "Optional shared password for the gateway. Do not fill both token and password.",
				},
				{
					Type:         bridgev2.LoginInputFieldTypeUsername,
					ID:           "label",
					Name:         "Gateway Label",
					Description:  "Optional label to distinguish multiple gateways.",
					DefaultValue: strings.TrimSpace(defaultLabel),
				},
			},
		},
	}
}

func normalizeOpenClawAuthCredentials(input map[string]string) (string, string, error) {
	token := strings.TrimSpace(input["token"])
	password := strings.TrimSpace(input["password"])
	if token != "" && password != "" {
		return "", "", errOpenClawMixedAuth
	}
	return token, password, nil
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
		return sdk.NewLoginRespError(http.StatusForbidden, msg, "OPENCLAW", "PAIRING_REQUIRED")
	case strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rpcErr.DetailCode)), "AUTH_"):
		return sdk.NewLoginRespError(http.StatusForbidden, rpcErr.Error(), "OPENCLAW", "AUTH_FAILED")
	default:
		return sdk.WrapLoginRespError(rpcErr, http.StatusInternalServerError, "OPENCLAW", "GATEWAY_REQUEST_FAILED")
	}
}

func normalizeOpenClawLoginURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", sdk.WrapLoginRespError(fmt.Errorf("invalid url: %w", err), http.StatusBadRequest, "OPENCLAW", "INVALID_URL")
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "ws"
	}
	if parsed.Host == "" {
		return "", errOpenClawMissingHost
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
