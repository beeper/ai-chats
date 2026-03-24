package openclaw

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/websocket"
	"maunium.net/go/mautrix/bridgev2/status"
)

const (
	openClawPairingRequiredError status.BridgeStateErrorCode = "openclaw-pairing-required"
	openClawAuthFailedError      status.BridgeStateErrorCode = "openclaw-auth-failed"
	openClawIncompatibleError    status.BridgeStateErrorCode = "openclaw-incompatible-gateway"
	openClawConnectError         status.BridgeStateErrorCode = "openclaw-connect-error"
	openClawTransientDisconnect  status.BridgeStateErrorCode = "openclaw-transient-disconnect"
	openClawGatewayClosedError   status.BridgeStateErrorCode = "openclaw-gateway-closed"
	openClawMaxReconnectDelay                                = time.Minute
)

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		openClawPairingRequiredError: "OpenClaw device pairing is required.",
		openClawAuthFailedError:      "OpenClaw authentication failed. Please relogin.",
		openClawIncompatibleError:    "OpenClaw gateway is incompatible with this bridge version.",
		openClawConnectError:         "Failed to connect to OpenClaw gateway. Retrying.",
		openClawTransientDisconnect:  "Disconnected from OpenClaw gateway. Retrying.",
		openClawGatewayClosedError:   "OpenClaw gateway closed the connection. Retrying.",
	})
}

type openClawCompatibilityError struct {
	Report openClawGatewayCompatibilityReport
}

func (e *openClawCompatibilityError) Error() string {
	if e == nil {
		return "OpenClaw gateway is incompatible"
	}
	parts := make([]string, 0, 3)
	if len(e.Report.MissingMethods) > 0 {
		parts = append(parts, "missing methods: "+strings.Join(e.Report.MissingMethods, ", "))
	}
	if len(e.Report.MissingEvents) > 0 {
		parts = append(parts, "missing events: "+strings.Join(e.Report.MissingEvents, ", "))
	}
	if !e.Report.HistoryEndpointOK {
		if e.Report.HistoryEndpointError != "" {
			parts = append(parts, "history endpoint: "+e.Report.HistoryEndpointError)
		} else if e.Report.HistoryEndpointCode != 0 {
			parts = append(parts, fmt.Sprintf("history endpoint: http %d", e.Report.HistoryEndpointCode))
		}
	}
	if len(parts) == 0 {
		return "OpenClaw gateway is incompatible"
	}
	return "OpenClaw gateway is incompatible: " + strings.Join(parts, "; ")
}

func openClawReconnectDelay(attempt int) time.Duration {
	attempt = max(attempt, 0)
	attempt = min(attempt, 6)
	return min(time.Second*time.Duration(1<<attempt), openClawMaxReconnectDelay)
}

func classifyOpenClawConnectionError(err error, retryDelay time.Duration) (status.BridgeState, bool) {
	state := status.BridgeState{
		StateEvent: status.StateTransientDisconnect,
		Error:      openClawTransientDisconnect,
		Message:    "Disconnected from OpenClaw gateway",
	}
	var rpcErr *gatewayRPCError
	var compatErr *openClawCompatibilityError
	switch {
	case errors.As(err, &compatErr):
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawIncompatibleError
		state.Message = strings.TrimSpace(err.Error())
		state.UserAction = status.UserActionRestart
		if compatErr != nil {
			state.Info = map[string]any{
				"server_version":           compatErr.Report.ServerVersion,
				"missing_methods":          compatErr.Report.MissingMethods,
				"missing_events":           compatErr.Report.MissingEvents,
				"required_missing_methods": compatErr.Report.RequiredMissingMethods,
				"required_missing_events":  compatErr.Report.RequiredMissingEvents,
				"history_endpoint_ok":      compatErr.Report.HistoryEndpointOK,
				"history_endpoint_code":    compatErr.Report.HistoryEndpointCode,
				"history_endpoint_err":     compatErr.Report.HistoryEndpointError,
			}
		}
		return state, false
	case errors.As(err, &rpcErr) && rpcErr.IsPairingRequired():
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawPairingRequiredError
		state.Message = strings.TrimSpace(rpcErr.Error())
		state.UserAction = status.UserActionRestart
		if strings.TrimSpace(rpcErr.RequestID) != "" {
			state.Info = map[string]any{"request_id": strings.TrimSpace(rpcErr.RequestID)}
		}
		return state, false
	case errors.As(err, &rpcErr) && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rpcErr.DetailCode)), "AUTH_"):
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawAuthFailedError
		state.Message = strings.TrimSpace(rpcErr.Error())
		return state, false
	}

	state.Info = map[string]any{
		"go_error": err.Error(),
	}
	if retryDelay > 0 {
		state.Info["retry_in_ms"] = retryDelay.Milliseconds()
	}
	if closeStatus := websocket.CloseStatus(err); closeStatus != -1 {
		state.Info["websocket_close_status"] = int(closeStatus)
		switch closeStatus {
		case websocket.StatusNormalClosure:
			state.Error = openClawGatewayClosedError
			state.Message = "OpenClaw gateway closed the connection"
		case websocket.StatusPolicyViolation:
			state.Error = openClawConnectError
			state.Message = "OpenClaw gateway rejected the connection"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "dial gateway websocket") {
		state.Error = openClawConnectError
		state.Message = "Failed to connect to OpenClaw gateway"
	}
	if retryDelay > 0 {
		state.Message = fmt.Sprintf("%s, retrying in %s", state.Message, retryDelay)
	} else {
		state.Message += ", retrying"
	}
	return state, true
}
