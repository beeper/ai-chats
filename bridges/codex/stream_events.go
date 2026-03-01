package codex

import (
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/matrixevents"
)

func buildStreamEventEnvelope(state *streamingState, part map[string]any) (turnID string, seq int, content map[string]any, ok bool) {
	turnID = strings.TrimSpace(state.turnID)
	if turnID == "" {
		return "", 0, nil, false
	}
	state.sequenceNum++
	seq = state.sequenceNum
	env, err := matrixevents.BuildStreamEventEnvelope(turnID, seq, part, matrixevents.StreamEventOpts{
		TargetEventID: state.initialEventID.String(),
		AgentID:       state.agentID,
	})
	if err != nil {
		return "", 0, nil, false
	}
	return turnID, seq, env, true
}

func defaultCodexChatPortalKey(loginID networkid.UserLoginID) networkid.PortalKey {
	return networkid.PortalKey{
		ID:       networkid.PortalID(fmt.Sprintf("codex:%s:default-chat", loginID)),
		Receiver: loginID,
	}
}
