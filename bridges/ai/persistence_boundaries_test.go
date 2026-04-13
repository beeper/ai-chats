package ai

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/sdk"
)

func newTranscriptTestPortal(t *testing.T, client *AIClient, portalID string) *bridgev2.Portal {
	t.Helper()

	ctx := context.Background()
	portalKey := networkid.PortalKey{
		ID:       networkid.PortalID(portalID),
		Receiver: client.UserLogin.ID,
	}
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: portalKey,
			MXID:      id.RoomID("!" + portalID + ":example.com"),
			Metadata:  &PortalMetadata{Slug: "chat-1"},
		},
		Bridge: client.UserLogin.Bridge,
	}
	if err := client.UserLogin.Bridge.DB.Portal.Insert(ctx, portal.Portal); err != nil {
		t.Fatalf("insert portal: %v", err)
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portalKey: portal,
	})
	setUnexportedField(client.UserLogin.Bridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{
		portal.MXID: portal,
	})
	return portal
}

func TestSaveUserMessage_PersistsConversationTurnOutsideBridgeMetadata(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portalKey := defaultChatPortalKey(client.UserLogin.ID)
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: portalKey,
			Metadata:  &PortalMetadata{Slug: "chat-1"},
		},
		Bridge: client.UserLogin.Bridge,
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portalKey: portal,
	})

	userMeta := &MessageMetadata{
		BaseMessageMetadata: sdk.BaseMessageMetadata{
			Role: "user",
			Body: "hello world",
		},
	}
	setCanonicalTurnDataFromPromptMessages(userMeta, []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: "hello world",
		}},
	}})
	msg := &database.Message{
		ID:        "msg-1",
		Room:      portalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Timestamp: time.UnixMilli(12345),
		Metadata:  userMeta,
	}
	evt := &event.Event{ID: "$event-1"}

	client.saveUserMessage(ctx, evt, msg)

	bridgeMsg, err := client.loadPortalMessagePartByMXID(ctx, portal, evt.ID)
	if err != nil {
		t.Fatalf("load bridge message row: %v", err)
	}
	if bridgeMsg == nil {
		t.Fatalf("expected bridge message row")
	}
	bridgeMeta, ok := bridgeMsg.Metadata.(*MessageMetadata)
	if !ok || bridgeMeta == nil {
		t.Fatalf("expected bridge message metadata, got %#v", bridgeMsg.Metadata)
	}
	if bridgeMeta.Role != "" || bridgeMeta.Body != "" || len(bridgeMeta.CanonicalTurnData) != 0 {
		t.Fatalf("expected bridge message metadata to stay transport-only, got %#v", bridgeMeta)
	}

	transcriptMsg, err := loadAIConversationMessage(ctx, portal, msg.ID, evt.ID)
	if err != nil {
		t.Fatalf("load persisted conversation message: %v", err)
	}
	if transcriptMsg == nil {
		t.Fatalf("expected persisted conversation message")
	}
	transcriptMeta, ok := transcriptMsg.Metadata.(*MessageMetadata)
	if !ok || transcriptMeta == nil {
		t.Fatalf("expected transcript metadata, got %#v", transcriptMsg.Metadata)
	}
	if transcriptMeta.Role != "user" || transcriptMeta.Body != "hello world" {
		t.Fatalf("expected conversation metadata to keep user payload, got %#v", transcriptMeta)
	}
	td, ok := canonicalTurnData(transcriptMeta)
	if !ok {
		t.Fatalf("expected canonical turn data to decode, got %#v", transcriptMeta.CanonicalTurnData)
	}
	if td.Role != "user" || sdk.TurnText(td) != "hello world" {
		t.Fatalf("expected canonical turn data to preserve visible user text, got %#v", td)
	}
}

func TestBuildBaseContext_ReplaysTranscriptHistoryFromFreshPortalLoad(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "transcript-history")

	userMeta := &MessageMetadata{
		BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: "hello world"},
	}
	setCanonicalTurnDataFromPromptMessages(userMeta, []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: "hello world",
		}},
	}})
	userMsg := &database.Message{
		ID:        sdk.MatrixMessageID(id.EventID("$user-1")),
		MXID:      id.EventID("$user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: id.EventID("$user-1")}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("assistant-1"),
		MXID:     id.EventID("$assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "turn-1",
					Role: "assistant",
					Parts: []sdk.TurnPart{{
						Type: "text",
						Text: "Hi there",
					}},
				}.ToMap(),
			},
		},
		Timestamp: time.UnixMilli(2000),
	}
	if err := persistAIConversationMessage(ctx, portal, assistantMsg); err != nil {
		t.Fatalf("persist assistant turn: %v", err)
	}

	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{})
	setUnexportedField(client.UserLogin.Bridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{})

	storedPortal, err := client.UserLogin.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		t.Fatalf("reload portal: %v", err)
	}
	if storedPortal == nil {
		t.Fatalf("expected reloaded portal")
	}
	freshPortal := &bridgev2.Portal{Portal: storedPortal, Bridge: client.UserLogin.Bridge}

	promptContext, err := client.buildBaseContext(ctx, freshPortal, portalMeta(freshPortal))
	if err != nil {
		t.Fatalf("buildBaseContext: %v", err)
	}
	if len(promptContext.Messages) != 2 {
		t.Fatalf("expected 2 replayed messages, got %d", len(promptContext.Messages))
	}
	if promptContext.Messages[0].Role != PromptRoleUser || promptContext.Messages[0].Text() != "hello world" {
		t.Fatalf("unexpected first replayed message: %#v", promptContext.Messages[0])
	}
	if promptContext.Messages[1].Role != PromptRoleAssistant || promptContext.Messages[1].Text() != "Hi there" {
		t.Fatalf("unexpected second replayed message: %#v", promptContext.Messages[1])
	}
}

func TestPortalScopeForPortal_StrictlyRequiresCanonicalBridgeID(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "portal-scope-strict")
	portal.Bridge.DB.BridgeID = ""

	if scope := portalScopeForPortal(portal); scope != nil {
		t.Fatalf("expected nil portal scope when canonical bridge id is missing, got %#v", scope)
	}
	if err := saveAIPortalState(ctx, portal, portalMeta(portal)); err != nil {
		t.Fatalf("strict portal state save should no-op without error, got %v", err)
	}
}

func TestHandleMatrixMessageRemove_DeletesTranscriptState(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "transcript-delete")
	msg := &database.Message{
		ID:        sdk.MatrixMessageID(id.EventID("$event-delete")),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Timestamp: time.UnixMilli(12345),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role:              "user",
				Body:              "delete me",
				CanonicalTurnData: map[string]any{"body": "delete me"},
			},
		},
	}
	evt := &event.Event{ID: id.EventID("$event-delete")}
	client.saveUserMessage(ctx, evt, msg)

	bridgeMsg, err := client.loadPortalMessagePartByMXID(ctx, portal, evt.ID)
	if err != nil {
		t.Fatalf("load bridge message row: %v", err)
	}
	if bridgeMsg == nil {
		t.Fatalf("expected bridge message row")
	}

	if err := client.HandleMatrixMessageRemove(ctx, &bridgev2.MatrixMessageRemove{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{
			Portal: portal,
		},
		TargetMessage: bridgeMsg,
	}); err != nil {
		t.Fatalf("HandleMatrixMessageRemove returned error: %v", err)
	}

	transcriptMsg, err := loadAIConversationMessage(ctx, portal, msg.ID, evt.ID)
	if err != nil {
		t.Fatalf("load turn after delete: %v", err)
	}
	if transcriptMsg != nil {
		t.Fatalf("expected turn to be deleted, got %#v", transcriptMsg)
	}

	history, err := client.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		t.Fatalf("load history after delete: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no history after delete, got %d entries", len(history))
	}
}

func TestSaveAIPortalState_DoesNotPersistBridgeRoomName(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			BridgeID:  client.UserLogin.Bridge.ID,
			PortalKey: defaultChatPortalKey(client.UserLogin.ID),
			Name:      "Bridge Owned Name",
		},
		Bridge: client.UserLogin.Bridge,
	}

	meta := &PortalMetadata{
		Slug:           "chat-1",
		TitleGenerated: true,
		WelcomeSent:    true,
	}
	portal.Metadata = meta
	if err := saveAIPortalState(ctx, portal, meta); err != nil {
		t.Fatalf("save portal state: %v", err)
	}

	loaded := &PortalMetadata{}
	loadPortalStateIntoMetadata(ctx, portal, loaded)

	if loaded.Slug != "chat-1" || !loaded.TitleGenerated || !loaded.WelcomeSent {
		t.Fatalf("expected AI-owned portal state to load, got %#v", loaded)
	}
	if portal.Name != "Bridge Owned Name" {
		t.Fatalf("expected bridge-owned room name to remain on the portal, got %q", portal.Name)
	}
}

func TestAdvanceAIPortalContextEpoch_HidesPreviousHistory(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "epoch-reset")
	meta := portalMeta(portal)
	if meta == nil {
		t.Fatal("expected portal metadata")
	}

	userMeta := &MessageMetadata{
		BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: "before reset"},
	}
	setCanonicalTurnDataFromPromptMessages(userMeta, []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: "before reset",
		}},
	}})
	userMsg := &database.Message{
		ID:        sdk.MatrixMessageID(id.EventID("$before-reset")),
		MXID:      id.EventID("$before-reset"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	record, err := loadAIPortalRecord(ctx, portal)
	if err != nil {
		t.Fatalf("load portal record before reset: %v", err)
	}
	if record == nil || record.ContextEpoch != 0 {
		t.Fatalf("expected initial context epoch 0, got %#v", record)
	}

	meta.SessionResetAt = time.Now().UnixMilli()
	if err := advanceAIPortalContextEpoch(ctx, portal); err != nil {
		t.Fatalf("advance context epoch: %v", err)
	}
	if err := saveAIPortalState(ctx, portal, meta); err != nil {
		t.Fatalf("save portal state after reset: %v", err)
	}

	record, err = loadAIPortalRecord(ctx, portal)
	if err != nil {
		t.Fatalf("load portal record after reset: %v", err)
	}
	if record == nil || record.ContextEpoch != 1 || record.NextTurnSequence != 0 {
		t.Fatalf("expected reset portal record, got %#v", record)
	}

	history, err := client.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		t.Fatalf("load history after reset: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("expected no visible history in new epoch, got %d entries", len(history))
	}

	turns, err := loadAIPromptHistoryTurns(ctx, portal, 10, historyReplayOptions{})
	if err != nil {
		t.Fatalf("load prompt turns after reset: %v", err)
	}
	if len(turns) != 0 {
		t.Fatalf("expected no replayable turns in new epoch, got %d", len(turns))
	}
}
