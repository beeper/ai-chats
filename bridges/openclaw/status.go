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
	openClawConnectError         status.BridgeStateErrorCode = "openclaw-connect-error"
	openClawTransientDisconnect  status.BridgeStateErrorCode = "openclaw-transient-disconnect"
	openClawGatewayClosedError   status.BridgeStateErrorCode = "openclaw-gateway-closed"
	openClawMaxReconnectDelay                                = time.Minute
)

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		openClawPairingRequiredError: "OpenClaw device pairing is required.",
		openClawAuthFailedError:      "OpenClaw authentication failed. Please relogin.",
		openClawConnectError:         "Failed to connect to OpenClaw gateway. Retrying.",
		openClawTransientDisconnect:  "Disconnected from OpenClaw gateway. Retrying.",
		openClawGatewayClosedError:   "OpenClaw gateway closed the connection. Retrying.",
	})
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
	switch {
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
