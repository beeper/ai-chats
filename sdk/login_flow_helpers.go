package sdk

import (
	"net/http"

	"maunium.net/go/mautrix/bridgev2"
)

func SingleLoginFlow(enabled bool, flow bridgev2.LoginFlow) []bridgev2.LoginFlow {
	if !enabled {
		return nil
	}
	return []bridgev2.LoginFlow{flow}
}

func ValidateSingleLoginFlow(flowID, expectedFlowID string, enabled bool) error {
	if flowID != expectedFlowID {
		return bridgev2.ErrInvalidLoginFlowID
	}
	if !enabled {
		return NewLoginRespError(http.StatusForbidden, "This login flow is disabled.", "LOGIN", "DISABLED")
	}
	return nil
}
