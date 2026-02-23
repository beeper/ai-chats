package codex

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func humanUserID(loginID networkid.UserLoginID) networkid.UserID {
	return bridgeadapter.HumanUserID("codex-user", loginID)
}

func ptrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return ptr.Ptr(value)
}

// Minimal room capabilities for codex bridge rooms.
var aiBaseCaps = &event.RoomFeatures{
	ID:                  aiCapabilityID,
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
}

func isMatrixBotUser(ctx context.Context, bridge *bridgev2.Bridge, userID id.UserID) bool {
	if userID == "" || bridge == nil {
		return false
	}
	if bridge.Bot != nil && bridge.Bot.GetMXID() == userID {
		return true
	}
	ghost, err := bridge.GetGhostByMXID(ctx, userID)
	return err == nil && ghost != nil
}

type approvalDecisionPayload struct {
	ApprovalID string
	Decision   string
	Reason     string
}

func readStringArgAny(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	raw := args[key]
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func parseApprovalDecision(raw map[string]any) *approvalDecisionPayload {
	if raw == nil {
		return nil
	}
	payloadRaw, ok := raw["com.beeper.ai.approval_decision"]
	if !ok || payloadRaw == nil {
		return nil
	}
	payloadMap, ok := payloadRaw.(map[string]any)
	if !ok {
		return nil
	}
	approvalID := readStringArgAny(payloadMap, "approvalId")
	decision := readStringArgAny(payloadMap, "decision")
	reason := readStringArgAny(payloadMap, "reason")
	if approvalID == "" || decision == "" {
		return nil
	}
	return &approvalDecisionPayload{
		ApprovalID: approvalID,
		Decision:   decision,
		Reason:     reason,
	}
}

func approvalDecisionFromString(decision string) (approve bool, always bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "approve", "yes", "y", "true", "1", "once":
		return true, false, true
	case "always", "always-allow", "allow-always":
		return true, true, true
	case "deny", "no", "n", "false", "0", "reject":
		return false, false, true
	default:
		return false, false, false
	}
}

func matrixEventTimestamp(evt *event.Event) time.Time {
	if evt != nil && evt.Timestamp > 0 {
		return time.UnixMilli(evt.Timestamp)
	}
	return time.Now()
}

func normalizeElevatedLevel(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	switch key {
	case "off", "false", "no", "0":
		return "off", true
	case "full", "auto", "auto-approve", "autoapprove":
		return "full", true
	case "ask", "prompt", "approval", "approve":
		return "ask", true
	case "on", "true", "yes", "1":
		return "on", true
	}
	return "", false
}
