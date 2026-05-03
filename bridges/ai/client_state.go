package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/sdk"
)

// ensureGhostDisplayName ensures the ghost has its display name set before sending messages.
// This fixes the issue where ghosts appear with raw user IDs instead of formatted names.
func (oc *AIClient) ensureGhostDisplayName(ctx context.Context, modelID string) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(modelID))
	if err != nil || ghost == nil {
		return
	}
	oc.ensureGhostDisplayNameWithGhost(ctx, ghost, modelID, oc.findModelInfo(modelID))
}

func (oc *AIClient) ensureGhostDisplayNameWithGhost(ctx context.Context, ghost *bridgev2.Ghost, modelID string, info *ModelInfo) {
	if ghost == nil {
		return
	}
	displayName := modelContactName(modelID, info)
	if ghost.Name == "" || !ghost.NameSet || ghost.Name != displayName {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:        ptr.Ptr(displayName),
			IsBot:       ptr.Ptr(false),
			Identifiers: modelContactIdentifiers(modelID),
		})
		oc.loggerForContext(ctx).Debug().Str("model", modelID).Str("name", displayName).Msg("Updated ghost display name")
	}
}

// ensureModelInRoom ensures the current portal sender ghost is joined to the portal room.
func (oc *AIClient) ensureModelInRoom(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil || portal.MXID == "" {
		return errors.New("invalid portal")
	}
	sender := oc.senderForPortal(ctx, portal)
	intent, ok := portal.GetIntentFor(ctx, sender, oc.UserLogin, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return errors.New("intent resolution failed")
	}
	return intent.EnsureJoined(ctx, portal.MXID)
}

func (oc *AIClient) loggerForContext(ctx context.Context) *zerolog.Logger {
	return sdk.LoggerFromContext(ctx, &oc.log)
}

func (oc *AIClient) backgroundContext(ctx context.Context) context.Context {
	var base context.Context
	// Use the per-login disconnectCtx so goroutines are cancelled on disconnect.
	if oc.disconnectCtx != nil {
		base = oc.disconnectCtx
	} else if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		base = oc.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}

	if model, ok := modelOverrideFromContext(ctx); ok {
		base = withModelOverride(base, model)
	}
	return oc.loggerForContext(ctx).WithContext(base)
}

// buildDedupeKey creates a unique key for inbound message deduplication.
// Format: matrix|{loginID}|{roomID}|{eventID}
func (oc *AIClient) buildDedupeKey(roomID id.RoomID, eventID id.EventID) string {
	return fmt.Sprintf("matrix|%s|%s|%s", oc.UserLogin.ID, roomID, eventID)
}

// handleDebouncedMessages processes flushed debounce buffer entries.
// This combines multiple rapid messages into a single AI request.
func (oc *AIClient) handleDebouncedMessages(entries []DebounceEntry) {
	if len(entries) == 0 {
		return
	}

	ctx := oc.backgroundContext(context.Background())
	last := entries[len(entries)-1]
	if last.Event == nil || last.Portal == nil {
		oc.loggerForContext(ctx).Warn().Msg("Skipping debounced batch with missing tail event or portal")
		return
	}
	if last.Meta != nil {
		if override := oc.effectiveModel(last.Meta); strings.TrimSpace(override) != "" {
			ctx = withModelOverride(ctx, override)
		}
	}

	// Combine raw bodies if multiple
	combinedRaw, _ := CombineDebounceEntries(entries)

	combinedBody := oc.buildMatrixInboundBody(ctx, last.Portal, last.Meta, last.Event, combinedRaw, last.SenderName, last.RoomName, last.IsGroup)
	inboundCtx := oc.buildMatrixInboundContext(last.Portal, last.Event, combinedRaw, last.SenderName, last.RoomName, last.IsGroup)
	ctx = withInboundContext(ctx, inboundCtx)
	rawEventContent := map[string]any(nil)
	if last.Event != nil && last.Event.Content.Raw != nil {
		rawEventContent = clonePendingRawMap(last.Event.Content.Raw)
	}
	pendingEvent := snapshotPendingEvent(last.Event)

	extraStatusEvents := make([]*event.Event, 0, len(entries)-1)
	if len(entries) > 1 {
		for _, entry := range entries[:len(entries)-1] {
			if entry.Event != nil {
				extraStatusEvents = append(extraStatusEvents, entry.Event)
			}
		}
	}
	statusEvents := queueStatusEvents(last.Event, extraStatusEvents)

	pending := pendingMessage{
		Event:           pendingEvent,
		Portal:          last.Portal,
		Meta:            last.Meta,
		InboundContext:  &inboundCtx,
		Type:            pendingTypeText,
		MessageBody:     combinedBody,
		StatusEvents:    statusEvents,
		PendingSent:     last.PendingSent,
		RawEventContent: rawEventContent,
		Typing: &TypingContext{
			IsGroup:      last.IsGroup,
			WasMentioned: last.WasMentioned,
		},
	}
	promptContext, err := oc.buildPromptContextForPendingMessage(ctx, pending, combinedBody)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to build prompt for debounced messages")
		oc.notifyMatrixSendFailure(ctx, last.Portal, last.Event, err)
		return
	}
	acceptedMessages := make([]*database.Message, 0, len(entries))
	for _, entry := range entries {
		if entry.Event == nil {
			continue
		}
		userMessage := entry.DBMessage
		if userMessage == nil {
			entryBody := oc.buildMatrixInboundBody(ctx, entry.Portal, entry.Meta, entry.Event, entry.RawBody, entry.SenderName, entry.RoomName, entry.IsGroup)
			userMessage = &database.Message{
				ID:       sdk.MatrixMessageID(entry.Event.ID),
				MXID:     entry.Event.ID,
				Room:     entry.Portal.PortalKey,
				SenderID: humanUserID(oc.UserLogin.ID),
				Metadata: &MessageMetadata{
					BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: entryBody},
				},
				Timestamp: sdk.MatrixEventTimestamp(entry.Event),
			}
		}
		if meta, ok := userMessage.Metadata.(*MessageMetadata); ok && len(meta.CanonicalTurnData) == 0 {
			meta.CanonicalTurnData = sdk.TurnData{
				Role: "user",
				Parts: []sdk.TurnPart{{
					Type: "text",
					Text: meta.Body,
				}},
			}.ToMap()
		}
		acceptedMessages = append(acceptedMessages, userMessage)
	}
	queueItem := pendingQueueItem{
		pending:          pending,
		acceptedMessages: acceptedMessages,
		messageID:        string(pendingEvent.ID),
		summaryLine:      combinedRaw,
		enqueuedAt:       time.Now().UnixMilli(),
	}
	var cfg *Config
	if oc != nil && oc.connector != nil {
		cfg = &oc.connector.Config
	}
	queueSettings := resolveQueueSettings(queueResolveParams{cfg: cfg, inlineOpts: airuntime.QueueInlineOptions{}})

	if err = oc.dispatchOrQueueCore(ctx, pendingEvent, last.Portal, last.Meta, queueItem, queueSettings, promptContext); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to dispatch debounced messages")
		oc.notifyMatrixSendFailure(ctx, last.Portal, last.Event, err)
	}

}
