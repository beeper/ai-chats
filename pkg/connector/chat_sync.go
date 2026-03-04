package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/beeper/ai-bridge/pkg/agents"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// createAgentChat creates a new chat room for an agent
func (oc *AIClient) createAgentChat(ctx context.Context, agent *agents.AgentDefinition) (*bridgev2.CreateChatResponse, error) {
	return oc.createAgentChatWithModel(ctx, agent, "", false)
}

func (oc *AIClient) createAgentChatWithModel(ctx context.Context, agent *agents.AgentDefinition, modelID string, applyModelOverride bool) (*bridgev2.CreateChatResponse, error) {
	if modelID == "" {
		modelID = oc.agentDefaultModel(agent)
	}

	agentName := oc.resolveAgentDisplayName(ctx, agent)
	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID:      modelID,
		Title:        fmt.Sprintf("Chat with %s", agentName),
		SystemPrompt: agent.SystemPrompt,
	})
	if err != nil {
		return nil, err
	}

	// Set agent-specific metadata
	pm := portalMeta(portal)
	pm.AgentID = agent.ID
	if agent.SystemPrompt != "" {
		pm.SystemPrompt = agent.SystemPrompt
	}
	if agent.ReasoningEffort != "" {
		pm.ReasoningEffort = agent.ReasoningEffort
	}
	if !applyModelOverride {
		pm.Model = ""
	}

	agentGhostID := agentUserID(agent.ID)

	// Update the OtherUserID to be the agent ghost
	portal.OtherUserID = agentGhostID
	agentAvatar := strings.TrimSpace(agent.AvatarURL)
	if agentAvatar == "" {
		agentAvatar = strings.TrimSpace(agents.DefaultAgentAvatarMXC)
	}
	if agentAvatar != "" {
		portal.AvatarID = networkid.AvatarID(agentAvatar)
		portal.AvatarMXC = id.ContentURIString(agentAvatar)
	}

	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to save portal with agent config: %w", err)
	}
	oc.ensureAgentGhostDisplayName(ctx, agent.ID, modelID, agentName)

	// Update chat info members to use agent ghost only
	oc.applyAgentChatInfo(chatInfo, agent.ID, agentName, modelID)

	// Rooms created via provisioning (ResolveIdentifier/CreateDM) won't go through our explicit
	// post-CreateMatrixRoom call sites. Schedule the welcome notice + auto-greeting for when the
	// Matrix room ID becomes available.
	oc.scheduleWelcomeMessage(ctx, portal.PortalKey)

	return &bridgev2.CreateChatResponse{
		PortalKey: portal.PortalKey,
		// Return the full ChatInfo so bridgev2 can apply ExtraUpdates (initial room state,
		// welcome notice, etc.) when creating the Matrix room via provisioning (CreateDM).
		PortalInfo: chatInfo,
	}, nil
}

// createNewChat creates a new portal for a specific model
func (oc *AIClient) createNewChat(ctx context.Context, modelID string) (*bridgev2.CreateChatResponse, error) {
	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID:      modelID,
		SystemPrompt: defaultSimpleModeSystemPrompt,
	})
	if err != nil {
		return nil, err
	}

	// Keep simple mode chats non-agentic by default.
	meta := portalMeta(portal)
	if meta != nil && !meta.IsSimpleMode {
		meta.IsSimpleMode = true
		if err := portal.Save(ctx); err != nil {
			return nil, fmt.Errorf("failed to save portal simple mode: %w", err)
		}
	}

	// Rooms created via provisioning (ResolveIdentifier/CreateDM) won't go through our explicit
	// post-CreateMatrixRoom call sites. Schedule the welcome notice for when the Matrix room exists.
	oc.scheduleWelcomeMessage(ctx, portal.PortalKey)

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		PortalInfo: chatInfo,
		Portal:     portal,
	}, nil
}

// handleFork creates a new chat and copies messages from the current conversation
func (oc *AIClient) handleFork(
	ctx context.Context,
	_ *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	arg string,
) {
	runCtx := oc.backgroundContext(ctx)

	// 1. Retrieve all messages from current chat
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(runCtx, portal.PortalKey, 10000)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't load messages: "+err.Error())
		return
	}

	if len(messages) == 0 {
		oc.sendSystemNotice(runCtx, portal, "No messages to fork.")
		return
	}

	// 2. If event ID specified, filter messages up to that point
	var messagesToCopy []*database.Message
	if arg != "" {
		// Validate Matrix event ID format
		if !strings.HasPrefix(arg, "$") {
			oc.sendSystemNotice(runCtx, portal, "Invalid event ID. Must start with '$'.")
			return
		}

		// Messages are newest-first, reverse iterate to find target
		found := false
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			messagesToCopy = append(messagesToCopy, msg)

			// Check MXID field (Matrix event ID)
			if msg.MXID != "" && string(msg.MXID) == arg {
				found = true
				break
			}
			// Check message ID format "mx:$eventid"
			if strings.HasSuffix(string(msg.ID), arg) {
				found = true
				break
			}
		}

		if !found {
			oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("Couldn't find event: %s", arg))
			return
		}
	} else {
		// Copy all messages (reverse to get chronological order)
		for i := len(messages) - 1; i >= 0; i-- {
			messagesToCopy = append(messagesToCopy, messages[i])
		}
	}

	// 3. Create new chat with same configuration
	newPortal, chatInfo, err := oc.createForkedChat(runCtx, portal, meta)
	if err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the forked chat: "+err.Error())
		return
	}

	// 4. Create Matrix room
	if err := newPortal.CreateMatrixRoom(runCtx, oc.UserLogin, chatInfo); err != nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	// 5. Copy messages to new chat
	copiedCount := oc.copyMessagesToChat(runCtx, newPortal, messagesToCopy)

	// 6. Send notice with link
	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(runCtx, portal, fmt.Sprintf(
		"Forked %d messages to new chat.\nOpen: %s",
		copiedCount, roomLink,
	))
}

// handleNewChat creates a new chat using the current room's agent/model,
// or an explicitly provided agent/model.
func (oc *AIClient) handleNewChat(
	ctx context.Context,
	_ *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	args []string,
) {
	runCtx := oc.backgroundContext(ctx)

	const usage = "Usage: !ai new [agent <agent_id>]"

	if len(args) >= 2 {
		cmd := strings.ToLower(args[0])
		if cmd != "agent" {
			oc.sendSystemNotice(runCtx, portal, usage)
			return
		}
		targetID := args[1]
		if targetID == "" || len(args) > 2 {
			oc.sendSystemNotice(runCtx, portal, usage)
			return
		}
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(runCtx, targetID)
		if err != nil || agent == nil {
			oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("Agent not found: %s", targetID))
			return
		}
		modelID, err := oc.resolveAgentModelForNewChat(runCtx, agent, "")
		if err != nil {
			oc.sendSystemNotice(runCtx, portal, err.Error())
			return
		}
		oc.createAndOpenAgentChat(runCtx, portal, agent, modelID, false)
		return
	} else if len(args) == 1 {
		oc.sendSystemNotice(runCtx, portal, usage)
		return
	}

	// No args: create new room of same type
	if meta == nil {
		oc.sendSystemNotice(runCtx, portal, "Couldn't read current room settings.")
		return
	}
	agentID := resolveAgentID(meta)
	if agentID != "" {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(runCtx, agentID)
		if err != nil || agent == nil {
			oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("Agent not found: %s", agentID))
			return
		}
		modelID, err := oc.resolveAgentModelForNewChat(runCtx, agent, oc.effectiveModel(meta))
		if err != nil {
			oc.sendSystemNotice(runCtx, portal, err.Error())
			return
		}
		modelOverride := meta != nil && meta.Model != ""
		oc.createAndOpenAgentChat(runCtx, portal, agent, modelID, modelOverride)
		return
	}

	modelID := oc.effectiveModel(meta)
	if modelID == "" {
		oc.sendSystemNotice(runCtx, portal, "No model configured for this room.")
		return
	}
	if ok, _ := oc.validateModel(runCtx, modelID); !ok {
		oc.sendSystemNotice(runCtx, portal, fmt.Sprintf("That model isn't available: %s", modelID))
		return
	}
	oc.createAndOpenSimpleChat(runCtx, portal, modelID)
}

func (oc *AIClient) resolveAgentModelForNewChat(ctx context.Context, agent *agents.AgentDefinition, preferredModel string) (string, error) {
	if preferredModel != "" {
		if ok, _ := oc.validateModel(ctx, preferredModel); ok {
			return preferredModel, nil
		}
	}

	if agent != nil {
		defaultModel := oc.agentDefaultModel(agent)
		if ok, _ := oc.validateModel(ctx, defaultModel); ok {
			return defaultModel, nil
		}
	}

	fallback := oc.effectiveModel(nil)
	if fallback != "" {
		if ok, _ := oc.validateModel(ctx, fallback); ok {
			return fallback, nil
		}
	}

	if preferredModel != "" {
		return "", fmt.Errorf("that model isn't available: %s", preferredModel)
	}
	return "", errors.New("no available model")
}

func (oc *AIClient) createAndOpenAgentChat(ctx context.Context, portal *bridgev2.Portal, agent *agents.AgentDefinition, modelID string, modelOverride bool) {
	agentName := oc.resolveAgentDisplayName(ctx, agent)
	chatResp, err := oc.createAgentChatWithModel(ctx, agent, modelID, modelOverride)
	if err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the chat: "+err.Error())
		return
	}

	newPortal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, chatResp.PortalKey)
	if err != nil || newPortal == nil {
		msg := "Couldn't open the new chat."
		if err != nil {
			msg = "Couldn't open the new chat: " + err.Error()
		}
		oc.sendSystemNotice(ctx, portal, msg)
		return
	}

	chatInfo := chatResp.PortalInfo
	if err := newPortal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	oc.sendWelcomeMessage(ctx, newPortal)

	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
		"New %s chat created.\nOpen: %s",
		agentName, roomLink,
	))
}

func (oc *AIClient) createAndOpenSimpleChat(ctx context.Context, portal *bridgev2.Portal, modelID string) {
	newPortal, chatInfo, err := oc.createNewSimpleChat(ctx, modelID)
	if err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the chat: "+err.Error())
		return
	}

	if err := newPortal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
		oc.sendSystemNotice(ctx, portal, "Couldn't create the room: "+err.Error())
		return
	}

	oc.sendWelcomeMessage(ctx, newPortal)

	roomLink := fmt.Sprintf("https://matrix.to/#/%s", newPortal.MXID)
	oc.sendSystemNotice(ctx, portal, fmt.Sprintf(
		"New %s chat created.\nOpen: %s",
		modelContactName(modelID, oc.findModelInfo(modelID)), roomLink,
	))
}

// createForkedChat creates a new portal inheriting config from source
func (oc *AIClient) createForkedChat(
	ctx context.Context,
	sourcePortal *bridgev2.Portal,
	sourceMeta *PortalMetadata,
) (*bridgev2.Portal, *bridgev2.ChatInfo, error) {
	sourceTitle := sourceMeta.Title
	if sourceTitle == "" {
		sourceTitle = sourcePortal.Name
	}
	title := fmt.Sprintf("%s (Fork)", sourceTitle)

	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		Title:    title,
		CopyFrom: sourceMeta,
	})
	if err != nil {
		return nil, nil, err
	}

	agentID := sourceMeta.AgentID
	if agentID != "" {
		pm := portalMeta(portal)
		pm.AgentID = agentID

		modelID := oc.effectiveModel(pm)
		portal.OtherUserID = agentUserID(agentID)

		agentName := agentID
		agentAvatar := ""
		// Try preset first - guaranteed to work for built-in agents (like "beeper")
		if preset := agents.GetPresetByID(agentID); preset != nil {
			agentName = oc.resolveAgentDisplayName(ctx, preset)
			agentAvatar = preset.AvatarURL
		} else {
			// Custom agent - need Matrix state lookup
			store := NewAgentStoreAdapter(oc)
			if agent, err := store.GetAgentByID(ctx, agentID); err == nil && agent != nil {
				agentName = oc.resolveAgentDisplayName(ctx, agent)
				agentAvatar = agent.AvatarURL
			}
		}
		if strings.TrimSpace(agentAvatar) == "" {
			agentAvatar = strings.TrimSpace(agents.DefaultAgentAvatarMXC)
		}
		if agentAvatar != "" {
			portal.AvatarID = networkid.AvatarID(agentAvatar)
			portal.AvatarMXC = id.ContentURIString(agentAvatar)
		}
		oc.applyAgentChatInfo(chatInfo, agentID, agentName, modelID)
		oc.ensureAgentGhostDisplayName(ctx, agentID, modelID, agentName)

		if err := portal.Save(ctx); err != nil {
			return nil, nil, err
		}
	}

	return portal, chatInfo, nil
}

// copyMessagesToChat queues messages to be bridged to the new chat
// Returns the count of successfully queued messages
func (oc *AIClient) copyMessagesToChat(
	ctx context.Context,
	destPortal *bridgev2.Portal,
	messages []*database.Message,
) int {
	copiedCount := 0
	skippedCount := 0

	for _, srcMsg := range messages {
		srcMeta := messageMeta(srcMsg)
		if srcMeta == nil || srcMeta.Body == "" {
			skippedCount++
			continue
		}

		// Determine sender
		var sender bridgev2.EventSender
		if srcMeta.Role == "user" {
			sender = bridgev2.EventSender{
				Sender:      humanUserID(oc.UserLogin.ID),
				SenderLogin: oc.UserLogin.ID,
				IsFromMe:    true,
			}
		} else {
			sender = bridgev2.EventSender{
				Sender:      srcMsg.SenderID,
				SenderLogin: oc.UserLogin.ID,
				IsFromMe:    false,
			}
		}

		// Create remote message for bridging
		remoteMsg := &OpenAIRemoteMessage{
			PortalKey: destPortal.PortalKey,
			ID:        networkid.MessageID(fmt.Sprintf("fork:%s", uuid.NewString())),
			Sender:    sender,
			Content:   srcMeta.Body,
			Timestamp: srcMsg.Timestamp,
			Metadata: &MessageMetadata{
				Role: srcMeta.Role,
				Body: srcMeta.Body,
			},
		}

		oc.UserLogin.QueueRemoteEvent(remoteMsg)
		copiedCount++
	}

	// Log if partial copy occurred (some messages were skipped)
	if skippedCount > 0 {
		oc.loggerForContext(ctx).Warn().
			Int("copied", copiedCount).
			Int("skipped", skippedCount).
			Int("total", len(messages)).
			Msg("Partial fork - some messages were skipped due to missing metadata")
	}

	return copiedCount
}

// createNewSimpleChat creates a new simple mode chat portal with the specified model.
func (oc *AIClient) createNewSimpleChat(ctx context.Context, modelID string) (*bridgev2.Portal, *bridgev2.ChatInfo, error) {
	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID:      modelID,
		SystemPrompt: defaultSimpleModeSystemPrompt,
	})
	if err != nil {
		return nil, nil, err
	}

	// Simple mode rooms are non-agentic. This disables directive processing.
	meta := portalMeta(portal)
	if meta != nil && !meta.IsSimpleMode {
		meta.IsSimpleMode = true
		if err := portal.Save(ctx); err != nil {
			return nil, nil, err
		}
	}

	return portal, chatInfo, nil
}

// Bootstrap and initialization functions

func (oc *AIClient) scheduleBootstrap() {
	backgroundCtx := oc.UserLogin.Bridge.BackgroundCtx
	oc.bootstrapOnce.Do(func() {
		go oc.bootstrap(backgroundCtx)
	})
}

func (oc *AIClient) bootstrap(ctx context.Context) {
	logCtx := oc.loggerForContext(ctx).With().Str("component", "openai-chat-bootstrap").Logger().WithContext(ctx)
	oc.waitForLoginPersisted(logCtx)

	meta := loginMetadata(oc.UserLogin)

	// Check if bootstrap already completed successfully
	if meta.ChatsSynced {
		oc.loggerForContext(ctx).Debug().Msg("Chats already synced, skipping bootstrap")
		// Still sync counter in case portals were created externally
		if err := oc.syncChatCounter(logCtx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to sync chat counter")
		}
		return
	}

	oc.loggerForContext(ctx).Info().Msg("Starting bootstrap for new login")

	if err := oc.syncChatCounter(logCtx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to sync chat counter, continuing with default chat creation")
		// Don't return - still create the default chat (matches other bridge patterns)
	}

	// Create default chat room with Beep agent
	if err := oc.ensureDefaultChat(logCtx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure default chat")
		// Continue anyway - default chat is optional
	}

	// Mark bootstrap as complete only after successful completion
	meta.ChatsSynced = true
	if err := oc.UserLogin.Save(logCtx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save ChatsSynced flag")
	} else {
		oc.loggerForContext(ctx).Info().Msg("Bootstrap completed successfully, ChatsSynced flag set")
	}
}

func (oc *AIClient) waitForLoginPersisted(ctx context.Context) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.After(60 * time.Second)
	for {
		_, err := oc.UserLogin.Bridge.DB.UserLogin.GetByID(ctx, oc.UserLogin.ID)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			oc.loggerForContext(ctx).Warn().Msg("Timed out waiting for login to persist, continuing anyway")
			return
		case <-ticker.C:
		}
	}
}

func (oc *AIClient) syncChatCounter(ctx context.Context) error {
	meta := loginMetadata(oc.UserLogin)
	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		return err
	}
	maxIdx := meta.NextChatIndex
	for _, portal := range portals {
		pm := portalMeta(portal)
		if idx, ok := parseChatSlug(pm.Slug); ok && idx > maxIdx {
			maxIdx = idx
		}
	}
	if maxIdx > meta.NextChatIndex {
		meta.NextChatIndex = maxIdx
		return oc.UserLogin.Save(ctx)
	}
	return nil
}

func (oc *AIClient) ensureDefaultChat(ctx context.Context) error {
	oc.loggerForContext(ctx).Debug().Msg("Ensuring default AI chat room exists")
	loginMeta := loginMetadata(oc.UserLogin)
	defaultPortalKey := defaultChatPortalKey(oc.UserLogin.ID)

	if loginMeta.DefaultChatPortalID != "" {
		portalKey := networkid.PortalKey{
			ID:       networkid.PortalID(loginMeta.DefaultChatPortalID),
			Receiver: oc.UserLogin.ID,
		}
		portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load default chat portal by ID")
		} else if portal != nil {
			if portal.MXID != "" {
				oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Existing default chat already has MXID")
				return nil
			}
			info := oc.chatInfoFromPortal(ctx, portal)
			oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("Default chat missing MXID; creating Matrix room")
			err := portal.CreateMatrixRoom(ctx, oc.UserLogin, info)
			if err != nil {
				oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
			}
			oc.sendWelcomeMessage(ctx, portal)
			return err
		}
	}

	if loginMeta.DefaultChatPortalID == "" {
		portal, err := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultPortalKey)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load default chat portal by deterministic key")
		} else if portal != nil {
			loginMeta.DefaultChatPortalID = string(portal.PortalKey.ID)
			if err := oc.UserLogin.Save(ctx); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist default chat portal ID")
			}
			if portal.MXID != "" {
				oc.loggerForContext(ctx).Debug().Stringer("portal", portal.PortalKey).Msg("Existing default chat already has MXID")
				return nil
			}
			info := oc.chatInfoFromPortal(ctx, portal)
			oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("Default chat missing MXID; creating Matrix room")
			err := portal.CreateMatrixRoom(ctx, oc.UserLogin, info)
			if err != nil {
				oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
			}
			oc.sendWelcomeMessage(ctx, portal)
			return err
		}
	}

	portals, err := oc.listAllChatPortals(ctx)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to list chat portals")
		return err
	}

	var defaultPortal *bridgev2.Portal
	var minIdx int
	for _, portal := range portals {
		pm := portalMeta(portal)
		if idx, ok := parseChatSlug(pm.Slug); ok {
			if defaultPortal == nil || idx < minIdx {
				minIdx = idx
				defaultPortal = portal
			}
		} else if defaultPortal == nil {
			defaultPortal = portal
		}
	}

	if defaultPortal != nil {
		loginMeta.DefaultChatPortalID = string(defaultPortal.PortalKey.ID)
		if err := oc.UserLogin.Save(ctx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist default chat portal ID")
		}
		if defaultPortal.MXID != "" {
			oc.loggerForContext(ctx).Debug().Stringer("portal", defaultPortal.PortalKey).Msg("Existing chat already has MXID")
			return nil
		}
		info := oc.chatInfoFromPortal(ctx, defaultPortal)
		oc.loggerForContext(ctx).Info().Stringer("portal", defaultPortal.PortalKey).Msg("Existing portal missing MXID; creating Matrix room")
		err := defaultPortal.CreateMatrixRoom(ctx, oc.UserLogin, info)
		if err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for existing portal")
		}
		oc.sendWelcomeMessage(ctx, defaultPortal)
		return err
	}

	// Create default chat with Beep agent
	beeperAgent := agents.GetBeeperAI()
	if beeperAgent == nil {
		return errors.New("beeper AI agent not found")
	}

	// Determine model from agent config or use default
	modelID := beeperAgent.Model.Primary
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	portal, chatInfo, err := oc.initPortalForChat(ctx, PortalInitOpts{
		ModelID:      modelID,
		Title:        "New AI Chat",
		SystemPrompt: beeperAgent.SystemPrompt,
		PortalKey:    &defaultPortalKey,
	})
	if err != nil {
		existingPortal, existingErr := oc.UserLogin.Bridge.GetExistingPortalByKey(ctx, defaultPortalKey)
		if existingErr == nil && existingPortal != nil {
			loginMeta.DefaultChatPortalID = string(existingPortal.PortalKey.ID)
			if err := oc.UserLogin.Save(ctx); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist default chat portal ID")
			}
			if existingPortal.MXID != "" {
				oc.loggerForContext(ctx).Debug().Stringer("portal", existingPortal.PortalKey).Msg("Existing default chat already has MXID")
				return nil
			}
			info := oc.chatInfoFromPortal(ctx, existingPortal)
			oc.loggerForContext(ctx).Info().Stringer("portal", existingPortal.PortalKey).Msg("Default chat missing MXID; creating Matrix room")
			createErr := existingPortal.CreateMatrixRoom(ctx, oc.UserLogin, info)
			if createErr != nil {
				oc.loggerForContext(ctx).Err(createErr).Msg("Failed to create Matrix room for default chat")
				return createErr
			}
			oc.sendWelcomeMessage(ctx, existingPortal)
			oc.loggerForContext(ctx).Info().Stringer("portal", existingPortal.PortalKey).Msg("New AI Chat room created")
			return nil
		}
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create default portal")
		return err
	}

	// Set agent-specific metadata
	pm := portalMeta(portal)
	pm.AgentID = beeperAgent.ID
	if beeperAgent.SystemPrompt != "" {
		pm.SystemPrompt = beeperAgent.SystemPrompt
	}

	// Update the OtherUserID to be the agent ghost
	agentGhostID := agentUserID(beeperAgent.ID)
	portal.OtherUserID = agentGhostID

	if err := portal.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to save portal with agent config")
		return err
	}

	// Update chat info members to use agent ghost only
	agentName := oc.resolveAgentDisplayName(ctx, beeperAgent)
	oc.applyAgentChatInfo(chatInfo, beeperAgent.ID, agentName, modelID)
	oc.ensureAgentGhostDisplayName(ctx, beeperAgent.ID, modelID, agentName)

	loginMeta.DefaultChatPortalID = string(portal.PortalKey.ID)
	if err := oc.UserLogin.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist default chat portal ID")
	}
	err = portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to create Matrix room for default chat")
		return err
	}
	oc.sendWelcomeMessage(ctx, portal)
	oc.loggerForContext(ctx).Info().Stringer("portal", portal.PortalKey).Msg("New AI Chat room created")
	return nil
}

func (oc *AIClient) listAllChatPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	// Query all portals and filter by receiver (our login ID)
	// This works because all our portals have Receiver set to our UserLogin.ID
	allDBPortals, err := oc.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	portals := make([]*bridgev2.Portal, 0)
	for _, dbPortal := range allDBPortals {
		// Filter to only portals owned by this user login
		if dbPortal.Receiver != oc.UserLogin.ID {
			continue
		}
		portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, dbPortal.PortalKey)
		if err != nil {
			return nil, err
		}
		if portal != nil {
			portals = append(portals, portal)
		}
	}
	return portals, nil
}
