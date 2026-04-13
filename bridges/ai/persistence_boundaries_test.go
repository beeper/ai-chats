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

func newTransientPortalWrapper(t *testing.T, client *AIClient, portal *bridgev2.Portal) *bridgev2.Portal {
	t.Helper()

	transientBridgeDB := *client.UserLogin.Bridge.DB
	transientBridgeDB.BridgeID = ""
	transientBridge := &bridgev2.Bridge{
		DB:     &transientBridgeDB,
		Config: client.UserLogin.Bridge.Config,
		Log:    client.UserLogin.Bridge.Log,
		Matrix: client.UserLogin.Bridge.Matrix,
	}
	setUnexportedField(transientBridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portal.PortalKey: portal,
	})
	setUnexportedField(transientBridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{
		portal.MXID: portal,
	})

	return &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
		Bridge: transientBridge,
	}
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

func TestBuildBaseContext_ReplaysHistoryFromTransientPortalByCanonicalizingPortalLookup(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "transient-history")

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
		ID:        sdk.MatrixMessageID(id.EventID("$transient-user-1")),
		MXID:      id.EventID("$transient-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("transient-assistant-1"),
		MXID:     id.EventID("$transient-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "transient-turn-1",
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

	transientPortal := newTransientPortalWrapper(t, client, portal)

	if scope := portalScopeForPortal(transientPortal); scope != nil {
		t.Fatalf("expected raw transient portal scope lookup to fail, got %#v", scope)
	}

	promptContext, err := client.buildBaseContext(ctx, transientPortal, portalMeta(transientPortal))
	if err != nil {
		t.Fatalf("buildBaseContext with transient portal: %v", err)
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

func TestPersistAIConversationMessageForClient_UsesCanonicalPortalScopeForTransientPortal(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "transient-write-scope")
	transientBridgeDB := *client.UserLogin.Bridge.DB
	transientBridgeDB.BridgeID = ""
	transientBridge := &bridgev2.Bridge{
		DB:     &transientBridgeDB,
		Config: client.UserLogin.Bridge.Config,
		Log:    client.UserLogin.Bridge.Log,
		Matrix: client.UserLogin.Bridge.Matrix,
	}
	setUnexportedField(transientBridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{})
	setUnexportedField(transientBridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{})
	transientPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
		Bridge: transientBridge,
	}

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
		ID:        sdk.MatrixMessageID(id.EventID("$transient-write-user-1")),
		MXID:      id.EventID("$transient-write-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}

	if scope := portalScopeForPortal(transientPortal); scope != nil {
		t.Fatalf("expected transient portal to be missing direct scope, got %#v", scope)
	}

	if err := client.persistAIConversationMessage(ctx, transientPortal, userMsg); err != nil {
		t.Fatalf("persist user turn via client wrapper: %v", err)
	}

	history, err := client.getAIHistoryMessages(ctx, transientPortal, 10)
	if err != nil {
		t.Fatalf("getAIHistoryMessages: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 replayed message, got %d", len(history))
	}
	meta := messageMeta(history[0])
	if meta == nil || meta.Role != "user" || meta.Body != "hello world" {
		t.Fatalf("unexpected persisted history metadata: %#v", meta)
	}
}

func TestBuildBaseContext_ReplaysHistoryFromCachedPortalWithoutEmbeddedBridgeID(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "cached-missing-bridge-id")

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
		ID:        sdk.MatrixMessageID(id.EventID("$cached-user-1")),
		MXID:      id.EventID("$cached-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("cached-assistant-1"),
		MXID:     id.EventID("$cached-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "cached-turn-1",
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

	cachedPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
		Bridge: client.UserLogin.Bridge,
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portal.PortalKey: cachedPortal,
	})
	setUnexportedField(client.UserLogin.Bridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{
		portal.MXID: cachedPortal,
	})

	promptContext, err := client.buildBaseContext(ctx, cachedPortal, portalMeta(cachedPortal))
	if err != nil {
		t.Fatalf("buildBaseContext with cached portal missing bridge id: %v", err)
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

func TestPortalScopeForPortal_UsesBridgeDatabaseBridgeID(t *testing.T) {
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "portal-scope-strict")
	portal.Bridge.ID = ""
	portal.Portal.BridgeID = ""

	scope := portalScopeForPortal(portal)
	if scope == nil {
		t.Fatal("expected portal scope from bridge database bridge id")
	}
	if scope.bridgeID != string(client.UserLogin.Bridge.DB.BridgeID) {
		t.Fatalf("expected bridge database bridge id %q, got %q", client.UserLogin.Bridge.DB.BridgeID, scope.bridgeID)
	}
}

func TestBuildBaseContext_ReplaysHistoryWhenPortalWrapperBridgeIsMissing(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "db-bridge-id-history")

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
		ID:        sdk.MatrixMessageID(id.EventID("$db-bridge-user-1")),
		MXID:      id.EventID("$db-bridge-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("db-bridge-assistant-1"),
		MXID:     id.EventID("$db-bridge-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "db-bridge-turn-1",
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

	transientPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
	}

	promptContext, err := client.buildBaseContext(ctx, transientPortal, portalMeta(transientPortal))
	if err != nil {
		t.Fatalf("buildBaseContext with missing portal bridge: %v", err)
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

func TestBuildBaseContext_ReplaysHistoryWhenBridgeCacheReturnsTransientPortal(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "transient-cache-history")

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
		ID:        sdk.MatrixMessageID(id.EventID("$transient-cache-user-1")),
		MXID:      id.EventID("$transient-cache-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("transient-cache-assistant-1"),
		MXID:     id.EventID("$transient-cache-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "transient-cache-turn-1",
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

	transientPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
	}
	setUnexportedField(client.UserLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{
		portal.PortalKey: transientPortal,
	})
	setUnexportedField(client.UserLogin.Bridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{
		portal.MXID: transientPortal,
	})

	promptContext, err := client.buildBaseContext(ctx, transientPortal, portalMeta(transientPortal))
	if err != nil {
		t.Fatalf("buildBaseContext with transient cached portal: %v", err)
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

func TestLoadAIPromptHistoryTurns_UsesCanonicalPortalScopeForTransientPortal(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "client-scoped-history")

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
		ID:        sdk.MatrixMessageID(id.EventID("$client-scope-user-1")),
		MXID:      id.EventID("$client-scope-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("client-scope-assistant-1"),
		MXID:     id.EventID("$client-scope-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "client-scope-turn-1",
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

	transientBridgeDB := *client.UserLogin.Bridge.DB
	transientBridgeDB.BridgeID = ""
	transientBridge := &bridgev2.Bridge{
		DB:     &transientBridgeDB,
		Config: client.UserLogin.Bridge.Config,
		Log:    client.UserLogin.Bridge.Log,
		Matrix: client.UserLogin.Bridge.Matrix,
	}
	transientPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
		Bridge: transientBridge,
	}

	if scope := portalScopeForPortal(transientPortal); scope != nil {
		t.Fatalf("expected transient portal scope lookup to fail, got %#v", scope)
	}

	turns, err := client.loadAIPromptHistoryTurns(ctx, transientPortal, 10, historyReplayOptions{})
	if err != nil {
		t.Fatalf("canonical portal-scoped history replay failed: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("expected 2 replayable turns, got %d", len(turns))
	}
	if turns[0].Role != "assistant" || sdk.TurnText(turns[0].TurnData) != "Hi there" {
		t.Fatalf("unexpected newest replayed turn: %#v", turns[0])
	}
	if turns[1].Role != "user" || sdk.TurnText(turns[1].TurnData) != "hello world" {
		t.Fatalf("unexpected second replayed turn: %#v", turns[1])
	}
}

func TestGetAIHistoryMessages_UsesCanonicalPortalScopeForTransientPortal(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "client-history-transient")

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
		ID:        sdk.MatrixMessageID(id.EventID("$client-history-user-1")),
		MXID:      id.EventID("$client-history-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	assistantMsg := &database.Message{
		ID:       networkid.MessageID("client-history-assistant-1"),
		MXID:     id.EventID("$client-history-assistant-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "Hi there",
				CanonicalTurnData: sdk.TurnData{
					ID:   "client-history-turn-1",
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

	transientBridgeDB := *client.UserLogin.Bridge.DB
	transientBridgeDB.BridgeID = ""
	transientBridge := &bridgev2.Bridge{
		DB:     &transientBridgeDB,
		Config: client.UserLogin.Bridge.Config,
		Log:    client.UserLogin.Bridge.Log,
		Matrix: client.UserLogin.Bridge.Matrix,
	}
	transientPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
		Bridge: transientBridge,
	}

	if scope := portalScopeForPortal(transientPortal); scope != nil {
		t.Fatalf("expected transient portal scope lookup to fail, got %#v", scope)
	}

	history, err := client.getAIHistoryMessages(ctx, transientPortal, 10)
	if err != nil {
		t.Fatalf("canonical portal-scoped history load failed: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(history))
	}
	if meta := messageMeta(history[0]); meta == nil || meta.Role != "assistant" || meta.Body != "Hi there" {
		t.Fatalf("unexpected first history message: %#v", history[0])
	}
	if meta := messageMeta(history[1]); meta == nil || meta.Role != "user" || meta.Body != "hello world" {
		t.Fatalf("unexpected second history message: %#v", history[1])
	}
}

func TestClientScopedTurnPersistence_WorksWithoutPortalBridge(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "client-scope-detached-portal")

	userMeta := &MessageMetadata{
		BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: "one"},
	}
	setCanonicalTurnDataFromPromptMessages(userMeta, []PromptMessage{{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: "one",
		}},
	}})
	userMsg := &database.Message{
		ID:        sdk.MatrixMessageID(id.EventID("$detached-user-1")),
		MXID:      id.EventID("$detached-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}

	detachedPortal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: portal.PortalKey,
			MXID:      portal.MXID,
			Metadata:  portal.Metadata,
		},
	}
	if scope := portalScopeForPortal(detachedPortal); scope != nil {
		t.Fatalf("expected detached portal scope lookup to fail, got %#v", scope)
	}

	if err := client.persistAIConversationMessage(ctx, detachedPortal, userMsg); err != nil {
		t.Fatalf("persist detached user turn via client wrapper: %v", err)
	}

	history, err := client.getAIHistoryMessages(ctx, detachedPortal, 10)
	if err != nil {
		t.Fatalf("history load through detached portal: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 replayed message, got %d", len(history))
	}
	if meta := messageMeta(history[0]); meta == nil || meta.Role != "user" || meta.Body != "one" {
		t.Fatalf("unexpected history message: %#v", history[0])
	}
}

func TestLoadAIConversationMessage_UsesCanonicalPortalScopeForTransientPortal(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "client-load-transient")

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
		ID:        sdk.MatrixMessageID(id.EventID("$client-load-user-1")),
		MXID:      id.EventID("$client-load-user-1"),
		Room:      portal.PortalKey,
		SenderID:  humanUserID(client.UserLogin.ID),
		Metadata:  userMeta,
		Timestamp: time.UnixMilli(1000),
	}
	client.saveUserMessage(ctx, &event.Event{ID: userMsg.MXID}, userMsg)

	transientPortal := newTransientPortalWrapper(t, client, portal)
	if scope := portalScopeForPortal(transientPortal); scope != nil {
		t.Fatalf("expected transient portal scope lookup to fail, got %#v", scope)
	}

	transcriptMsg, err := client.loadAIConversationMessage(ctx, transientPortal, userMsg.ID, userMsg.MXID)
	if err != nil {
		t.Fatalf("canonical portal-scoped conversation load failed: %v", err)
	}
	if transcriptMsg == nil {
		t.Fatal("expected transcript message")
	}
	if meta := messageMeta(transcriptMsg); meta == nil || meta.Role != "user" || meta.Body != "hello world" {
		t.Fatalf("unexpected transcript message: %#v", transcriptMsg)
	}
}

func TestLoadAIPromptHistoryTurnsByScope_MissingScopeReturnsNoHistory(t *testing.T) {
	ctx := context.Background()
	portal := &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey: networkid.PortalKey{
				ID:       networkid.PortalID("missing-scope"),
				Receiver: networkid.UserLoginID("login-1"),
			},
		},
	}

	turns, err := loadAIPromptHistoryTurnsByScope(ctx, nil, portal, historyReplayOptions{}, 10)
	if err != nil {
		t.Fatalf("expected missing scope to be non-fatal, got %v", err)
	}
	if len(turns) != 0 {
		t.Fatalf("expected no turns without scope, got %d", len(turns))
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

	portal, err := client.UserLogin.Bridge.GetPortalByKey(ctx, defaultChatPortalKey(client.UserLogin.ID))
	if err != nil {
		t.Fatalf("create portal: %v", err)
	}
	portal.Name = "Bridge Owned Name"

	meta := &PortalMetadata{
		Slug:           "chat-1",
		TitleGenerated: true,
		WelcomeSent:    true,
	}
	portal.Metadata = meta
	if err := portal.Save(ctx); err != nil {
		t.Fatalf("save portal state: %v", err)
	}

	reloaded, err := client.UserLogin.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		t.Fatalf("reload portal: %v", err)
	}
	loaded, _ := reloaded.Metadata.(*PortalMetadata)

	if portalMeta(portal).Slug != "chat-1" {
		t.Fatalf("expected slug to persist through portal metadata, got %#v", portalMeta(portal))
	}
	if loaded == nil || loaded.Slug != "chat-1" || !loaded.TitleGenerated || !loaded.WelcomeSent {
		t.Fatalf("expected AI portal metadata to reload from portal metadata, got %#v", loaded)
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
	if err := portal.Save(ctx); err != nil {
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

func TestWaitForAssistantTurnAfter_UsesCanonicalSequenceInsteadOfTimestamp(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "assistant-sequence-order")

	first := &database.Message{
		ID:       networkid.MessageID("assistant-seq-1"),
		MXID:     id.EventID("$assistant-seq-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "first",
				CanonicalTurnData: sdk.TurnData{
					ID:   "assistant-turn-1",
					Role: "assistant",
					Parts: []sdk.TurnPart{{
						Type: "text",
						Text: "first",
					}},
				}.ToMap(),
			},
		},
		Timestamp: time.UnixMilli(2_000),
	}
	if err := persistAIConversationMessage(ctx, portal, first); err != nil {
		t.Fatalf("persist first assistant turn: %v", err)
	}

	checkpoint := client.lastAssistantTurnCheckpoint(ctx, portal)
	if checkpoint.TurnID != "assistant-turn-1" || checkpoint.Sequence == 0 {
		t.Fatalf("unexpected checkpoint after first turn: %#v", checkpoint)
	}

	second := &database.Message{
		ID:       networkid.MessageID("assistant-seq-2"),
		MXID:     id.EventID("$assistant-seq-2"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "second",
				CanonicalTurnData: sdk.TurnData{
					ID:   "assistant-turn-2",
					Role: "assistant",
					Parts: []sdk.TurnPart{{
						Type: "text",
						Text: "second",
					}},
				}.ToMap(),
			},
		},
		// Intentionally earlier than the first turn. Canonical ordering must still
		// follow turn sequence, not raw timestamps.
		Timestamp: time.UnixMilli(1_000),
	}
	if err := persistAIConversationMessage(ctx, portal, second); err != nil {
		t.Fatalf("persist second assistant turn: %v", err)
	}

	msg, found := client.waitForAssistantTurnAfter(ctx, portal, checkpoint)
	if !found || msg == nil {
		t.Fatal("expected to find assistant turn after checkpoint")
	}
	meta := messageMeta(msg)
	if meta == nil || meta.Body != "second" {
		t.Fatalf("expected second assistant turn, got %#v", meta)
	}
}

func TestWaitForAssistantTurnAfter_AcceptsNewEpochWithResetSequence(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	portal := newTranscriptTestPortal(t, client, "assistant-epoch-order")

	beforeReset := &database.Message{
		ID:       networkid.MessageID("assistant-epoch-1"),
		MXID:     id.EventID("$assistant-epoch-1"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "before reset",
				CanonicalTurnData: sdk.TurnData{
					ID:   "assistant-epoch-turn-1",
					Role: "assistant",
					Parts: []sdk.TurnPart{{
						Type: "text",
						Text: "before reset",
					}},
				}.ToMap(),
			},
		},
		Timestamp: time.UnixMilli(5_000),
	}
	if err := persistAIConversationMessage(ctx, portal, beforeReset); err != nil {
		t.Fatalf("persist assistant turn before reset: %v", err)
	}

	checkpoint := client.lastAssistantTurnCheckpoint(ctx, portal)
	if checkpoint.ContextEpoch != 0 || checkpoint.Sequence == 0 {
		t.Fatalf("unexpected checkpoint before reset: %#v", checkpoint)
	}

	if err := advanceAIPortalContextEpoch(ctx, portal); err != nil {
		t.Fatalf("advance context epoch: %v", err)
	}

	afterReset := &database.Message{
		ID:       networkid.MessageID("assistant-epoch-2"),
		MXID:     id.EventID("$assistant-epoch-2"),
		Room:     portal.PortalKey,
		SenderID: modelUserID("openai/gpt-4.1"),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{
				Role: "assistant",
				Body: "after reset",
				CanonicalTurnData: sdk.TurnData{
					ID:   "assistant-epoch-turn-2",
					Role: "assistant",
					Parts: []sdk.TurnPart{{
						Type: "text",
						Text: "after reset",
					}},
				}.ToMap(),
			},
		},
		Timestamp: time.UnixMilli(1_000),
	}
	if err := persistAIConversationMessage(ctx, portal, afterReset); err != nil {
		t.Fatalf("persist assistant turn after reset: %v", err)
	}

	msg, found := client.waitForAssistantTurnAfter(ctx, portal, checkpoint)
	if !found || msg == nil {
		t.Fatal("expected to find assistant turn in newer context epoch")
	}
	meta := messageMeta(msg)
	if meta == nil || meta.Body != "after reset" {
		t.Fatalf("expected post-reset assistant turn, got %#v", meta)
	}
}
