package bridgeadapter

import (
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func MatrixMessageID(eventID id.EventID) networkid.MessageID {
	return networkid.MessageID(fmt.Sprintf("mx:%s", string(eventID)))
}

func HumanUserID(prefix string, loginID networkid.UserLoginID) networkid.UserID {
	return networkid.UserID(fmt.Sprintf("%s:%s", prefix, loginID))
}

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
