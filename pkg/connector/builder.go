package connector

import (
	"context"
	"fmt"

	"go.mau.fi/util/ptr"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/ai-bridge/pkg/agents"
)

// Builder room constants
const (
	BuilderRoomSlug = "builder"
	BuilderRoomName = "Manage AI Chats"
)

// ensureBuilderRoom creates or retrieves the "Manage AI Chats" room.
// This special room is where users interact with the Boss agent to manage their agents and rooms.
func (oc *AIClient) ensureBuilderRoom(ctx context.Context) error {
	meta := loginMetadata(oc.UserLogin)

	// Check if we already have a Builder room
	if meta.BuilderRoomID != "" {
		// Verify it still exists
		portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, networkid.PortalKey{
			ID:       meta.BuilderRoomID,
			Receiver: oc.UserLogin.ID,
		})
		if err == nil && portal != nil && portal.MXID != "" {
			oc.loggerForContext(ctx).Debug().Str("room_id", string(meta.BuilderRoomID)).Msg("Manage AI Chats room already exists")
			return nil
		}
		// Room doesn't exist anymore, clear the reference
		meta.BuilderRoomID = ""
	}

	oc.loggerForContext(ctx).Info().Msg("Creating Manage AI Chats room")

	// Create the Builder room with Boss agent as the ghost
	portal, chatInfo, err := oc.createBuilderRoom(ctx)
	if err != nil {
		return fmt.Errorf("failed to create builder room: %w", err)
	}

	// Create Matrix room
	if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
		cleanupPortal(ctx, oc, portal, "failed to create builder Matrix room")
		return fmt.Errorf("failed to create matrix room for builder: %w", err)
	}

	// Send welcome message (excluded from LLM history)
	oc.sendWelcomeMessage(ctx, portal)

	// Store the Builder room ID
	meta.BuilderRoomID = portal.PortalKey.ID
	if err := oc.UserLogin.Save(ctx); err != nil {
		meta.BuilderRoomID = ""
		cleanupPortal(ctx, oc, portal, "failed to save BuilderRoomID")
		return fmt.Errorf("failed to save BuilderRoomID: %w", err)
	}

	oc.loggerForContext(ctx).Info().
		Str("portal_id", string(portal.PortalKey.ID)).
		Str("mxid", string(portal.MXID)).
		Msg("Manage AI Chats room created")

	return nil
}

// createBuilderRoom creates the "Manage AI Chats" room portal and chat info.
func (oc *AIClient) createBuilderRoom(ctx context.Context) (*bridgev2.Portal, *bridgev2.ChatInfo, error) {
	bossAgent := agents.GetBossAgent()

	// Use a standard chat initialization with the management room title
	opts := PortalInitOpts{
		Title: BuilderRoomName,
	}

	portal, chatInfo, err := oc.initPortalForChat(ctx, opts)
	if err != nil {
		return nil, nil, err
	}

	// Set up the portal metadata for the Boss agent
	pm := portalMeta(portal)
	pm.Slug = BuilderRoomSlug // Override slug to "builder"
	pm.AgentID = bossAgent.ID
	pm.SystemPrompt = agents.BossSystemPrompt
	pm.Model = bossAgent.Model.Primary // Explicit model - always use Boss agent's model
	pm.IsBuilderRoom = true            // Mark as protected from overrides

	// Use agent ghost for the Boss agent
	modelID := pm.Model
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}
	bossGhostID := agentUserID(bossAgent.ID)
	bossDisplayName := oc.resolveAgentDisplayName(ctx, modelID)
	portal.OtherUserID = bossGhostID

	if chatInfo != nil && chatInfo.Members != nil {
		members := chatInfo.Members
		if members.MemberMap == nil {
			members.MemberMap = make(bridgev2.ChatMemberMap)
		}
		members.OtherUserID = bossGhostID
		humanID := humanUserID(oc.UserLogin.ID)
		humanMember := members.MemberMap[humanID]
		humanMember.EventSender = bridgev2.EventSender{
			IsFromMe:    true,
			SenderLogin: oc.UserLogin.ID,
		}
		bossMember := members.MemberMap[bossGhostID]
		bossMember.EventSender = bridgev2.EventSender{
			Sender:      bossGhostID,
			SenderLogin: oc.UserLogin.ID,
		}
		bossMember.UserInfo = &bridgev2.UserInfo{
			Name:        ptr.Ptr(bossDisplayName),
			IsBot:       ptr.Ptr(true),
			Identifiers: modelContactIdentifiers(modelID, oc.findModelInfo(modelID)),
		}
		bossMember.MemberEventExtra = map[string]any{
			"displayname":            bossDisplayName,
			"com.beeper.ai.model_id": modelID,
			"com.beeper.ai.agent":    bossAgent.ID,
		}
		members.MemberMap = bridgev2.ChatMemberMap{
			humanID:     humanMember,
			bossGhostID: bossMember,
		}
		chatInfo.Members = members
	}

	// Re-save portal with updated metadata
	if err := portal.Save(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to save portal with agent config: %w", err)
	}

	return portal, chatInfo, nil
}

// isBuilderRoom checks if a portal is the Builder room.
func (oc *AIClient) isBuilderRoom(portal *bridgev2.Portal) bool {
	meta := loginMetadata(oc.UserLogin)
	return meta.BuilderRoomID != "" && portal.PortalKey.ID == meta.BuilderRoomID
}
