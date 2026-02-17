package bridgeadapter

import (
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
)

func SingleLoginFlow(enabled bool, flow bridgev2.LoginFlow) []bridgev2.LoginFlow {
	if !enabled {
		return nil
	}
	return []bridgev2.LoginFlow{flow}
}

func ValidateSingleLoginFlow(flowID, expectedFlowID string, enabled bool) error {
	if flowID != expectedFlowID || !enabled {
		return fmt.Errorf("login flow %s is not available", flowID)
	}
	return nil
}
