package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func TestEditModeNextTurnDeletesOnlyNextAssistantTurn(t *testing.T) {
	ctx := context.Background()
	oc := newDBBackedTestAIClient(t, ProviderOpenAI)
	portal := testAIModelPortal(t, oc, "openai/gpt-5.4")
	portal.MXID = id.RoomID("!edit-next:example.com")

	first := testTurnMessage(portal, "$one", "user", "one", 1)
	second := testTurnMessage(portal, "$two", "assistant", "two", 2)
	third := testTurnMessage(portal, "$three", "user", "three", 3)
	fourth := testTurnMessage(portal, "$four", "assistant", "four", 4)
	for _, msg := range []*database.Message{first, second, third, fourth} {
		if err := oc.persistAIConversationMessage(ctx, portal, msg); err != nil {
			t.Fatalf("persist %s: %v", msg.MXID, err)
		}
	}

	removed, err := oc.deleteAINextAssistantTurnAfterExternalRef(ctx, portal, first.ID, first.MXID)
	if err != nil {
		t.Fatalf("delete next assistant after edit: %v", err)
	}
	if len(removed) != 1 || removed[0].EventID != second.MXID {
		t.Fatalf("expected only next assistant turn removed, got %#v", removed)
	}
	history, err := oc.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 3 || history[0].MXID != fourth.MXID || history[1].MXID != third.MXID || history[2].MXID != first.MXID {
		t.Fatalf("expected later turns except next assistant to remain, got %#v", history)
	}
}

func TestEditModeAllNextsTruncatesSubsequentTurns(t *testing.T) {
	ctx := context.Background()
	oc := newDBBackedTestAIClient(t, ProviderOpenAI)
	portal := testAIModelPortal(t, oc, "openai/gpt-5.4")
	portal.MXID = id.RoomID("!edit-all:example.com")

	first := testTurnMessage(portal, "$one", "user", "one", 1)
	second := testTurnMessage(portal, "$two", "assistant", "two", 2)
	third := testTurnMessage(portal, "$three", "user", "three", 3)
	fourth := testTurnMessage(portal, "$four", "assistant", "four", 4)
	for _, msg := range []*database.Message{first, second, third, fourth} {
		if err := oc.persistAIConversationMessage(ctx, portal, msg); err != nil {
			t.Fatalf("persist %s: %v", msg.MXID, err)
		}
	}

	removed, err := oc.deleteAITurnsAfterExternalRef(ctx, portal, first.ID, first.MXID)
	if err != nil {
		t.Fatalf("truncate after edit: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("expected three removed turns, got %d", len(removed))
	}
	history, err := oc.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 1 || history[0].MXID != first.MXID {
		t.Fatalf("expected only edited turn to remain, got %#v", history)
	}
}

func TestEditModeNormalization(t *testing.T) {
	meta := &PortalMetadata{EditMode: editModeAllNexts}
	if got := resolveEditMode(meta); got != editModeAllNexts {
		t.Fatalf("expected resolved all-nexts edit mode, got %q", got)
	}
	if got := normalizeEditMode("everything"); got != "" {
		t.Fatalf("expected invalid mode to normalize empty, got %q", got)
	}
	if got := resolveEditMode(&PortalMetadata{}); got != editModeNextTurn {
		t.Fatalf("expected next-turn default, got %q", got)
	}
}

func TestModelRoomCapabilitiesAllowEdits(t *testing.T) {
	oc := newTestAIClientWithProvider(ProviderOpenRouter)
	caps := oc.GetCapabilities(context.Background(), capabilityTestPortal("openai/gpt-5.4"))
	if caps.Edit != event.CapLevelFullySupported {
		t.Fatalf("expected edits to be supported, got %v", caps.Edit)
	}
}

func testTurnMessage(portal *bridgev2.Portal, eventID id.EventID, role, body string, second int) *database.Message {
	turnData := sdk.TurnData{
		Role: role,
		ID:   string(eventID),
		Parts: []sdk.TurnPart{{
			Type: "text",
			Text: body,
		}},
	}
	return &database.Message{
		ID:        sdk.MatrixMessageID(eventID),
		MXID:      eventID,
		Room:      portal.PortalKey,
		SenderID:  humanUserID("login"),
		Timestamp: time.Unix(int64(second), 0),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role:              role,
				Body:              body,
				CanonicalTurnData: turnData.ToMap(),
			},
		},
	}
}
