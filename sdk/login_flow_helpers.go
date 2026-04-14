package sdk

import (
	"net/http"

	"maunium.net/go/mautrix/bridgev2"
)

func ValidateLoginFlow(
	flowID string,
	enabled bool,
	disabledMessage string,
	errNamespace string,
	errCode string,
	allowed func(string) bool,
) error {
	if !enabled {
		return NewLoginRespError(http.StatusForbidden, disabledMessage, errNamespace, errCode)
	}
	if allowed == nil || !allowed(flowID) {
		return bridgev2.ErrInvalidLoginFlowID
	}
	return nil
}
