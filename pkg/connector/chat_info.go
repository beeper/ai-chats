package connector

import (
	"context"
	"errors"
	"time"

	"go.mau.fi/util/ptr"

	"github.com/beeper/ai-bridge/pkg/agents"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// chatInfoFromPortal builds ChatInfo from an existing portal
func (oc *AIClient) chatInfoFromPortal(ctx context.Context, portal *bridgev2.Portal) *bridgev2.ChatInfo {
	meta := portalMeta(portal)
	modelID := oc.effectiveModel(meta)
	title := meta.Title
	if title == "" {
		if portal.Name != "" {
			title = portal.Name
		} else {
			title = modelContactName(modelID, oc.findModelInfo(modelID))
		}
	}
	chatInfo := oc.composeChatInfo(title, modelID)

	agentID := resolveAgentID(meta)
	if agentID == "" {
		return chatInfo
	}

	agentName := agentID
	// Try preset first - guaranteed to work for built-in agents (like "beeper")
	if preset := agents.GetPresetByID(agentID); preset != nil {
		agentName = oc.resolveAgentDisplayName(ctx, preset)
	} else if ctx != nil {
		// Custom agent - need Matrix state lookup
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil && agent != nil {
			agentName = oc.resolveAgentDisplayName(ctx, agent)
		}
	}

	oc.applyAgentChatInfo(chatInfo, agentID, agentName, modelID)
	return chatInfo
}

// composeChatInfo creates a ChatInfo struct for a chat
func (oc *AIClient) composeChatInfo(title, modelID string) *bridgev2.ChatInfo {
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}
	modelInfo := oc.findModelInfo(modelID)
	modelName := modelContactName(modelID, modelInfo)
	if title == "" {
		title = modelName
	}
	members := bridgev2.ChatMemberMap{
		humanUserID(oc.UserLogin.ID): {
			EventSender: bridgev2.EventSender{
				IsFromMe:    true,
				SenderLogin: oc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
		},
		modelUserID(modelID): {
			EventSender: bridgev2.EventSender{
				Sender:      modelUserID(modelID),
				SenderLogin: oc.UserLogin.ID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:        ptr.Ptr(modelName),
				IsBot:       ptr.Ptr(false),
				Identifiers: modelContactIdentifiers(modelID, modelInfo),
			},
			// Set displayname directly in membership event content
			// This works because MemberEventContent.Displayname has omitempty
			MemberEventExtra: map[string]any{
				"displayname":            modelName,
				"com.beeper.ai.model_id": modelID,
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: nil, // Topic managed via Matrix events, not system prompt
		Type:  ptr.Ptr(database.RoomTypeDM),
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: modelUserID(modelID),
			MemberMap:   members,
			// Set power levels so only bridge bot can modify room_capabilities (100)
			// while any user can modify room_settings (0)
			PowerLevels: &bridgev2.PowerLevelOverrides{
				Events: map[event.Type]int{
					RoomCapabilitiesEventType: 100, // Only bridge bot
					RoomSettingsEventType:     0,   // Any user
				},
			},
		},
	}
}

func (oc *AIClient) applyAgentChatInfo(chatInfo *bridgev2.ChatInfo, agentID, agentName, modelID string) {
	if chatInfo == nil || agentID == "" {
		return
	}
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	agentGhostID := agentUserID(agentID)
	agentDisplayName := agentName

	members := chatInfo.Members
	if members == nil {
		members = &bridgev2.ChatMemberList{}
	}
	if members.MemberMap == nil {
		members.MemberMap = make(bridgev2.ChatMemberMap)
	}
	members.OtherUserID = agentGhostID

	humanID := humanUserID(oc.UserLogin.ID)
	humanMember := members.MemberMap[humanID]
	humanMember.EventSender = bridgev2.EventSender{
		IsFromMe:    true,
		SenderLogin: oc.UserLogin.ID,
	}

	agentMember := members.MemberMap[agentGhostID]
	agentMember.EventSender = bridgev2.EventSender{
		Sender:      agentGhostID,
		SenderLogin: oc.UserLogin.ID,
	}
	modelInfo := oc.findModelInfo(modelID)
	agentMember.UserInfo = &bridgev2.UserInfo{
		Name:        ptr.Ptr(agentDisplayName),
		IsBot:       ptr.Ptr(true),
		Identifiers: agentContactIdentifiers(agentID, modelID, modelInfo),
	}
	agentMember.MemberEventExtra = map[string]any{
		"displayname":            agentDisplayName,
		"com.beeper.ai.model_id": modelID,
		"com.beeper.ai.agent":    agentID,
	}

	members.MemberMap = bridgev2.ChatMemberMap{
		humanID:      humanMember,
		agentGhostID: agentMember,
	}
	chatInfo.Members = members
}

// BroadcastRoomState sends current room capabilities and settings to Matrix room state
func (oc *AIClient) BroadcastRoomState(ctx context.Context, portal *bridgev2.Portal) error {
	if err := oc.broadcastCapabilities(ctx, portal); err != nil {
		return err
	}
	if err := oc.broadcastSettings(ctx, portal); err != nil {
		return err
	}
	// Broadcast command descriptions so clients can discover slash commands.
	oc.BroadcastCommandDescriptions(ctx, portal)
	return nil
}

// buildEffectiveSettings builds the effective settings with source explanations
func (oc *AIClient) buildEffectiveSettings(meta *PortalMetadata) *EffectiveSettings {
	loginMeta := loginMetadata(oc.UserLogin)

	return &EffectiveSettings{
		Model:           oc.getModelWithSource(meta, loginMeta),
		SystemPrompt:    oc.getPromptWithSource(meta, loginMeta),
		Temperature:     oc.getTempWithSource(meta, loginMeta),
		ReasoningEffort: oc.getReasoningWithSource(meta, loginMeta),
	}
}

func (oc *AIClient) getModelWithSource(meta *PortalMetadata, loginMeta *UserLoginMetadata) SettingExplanation {
	if meta != nil && meta.Model != "" {
		return SettingExplanation{Value: meta.Model, Source: SourceRoomOverride}
	}
	if loginMeta.Defaults != nil && loginMeta.Defaults.Model != "" {
		return SettingExplanation{Value: loginMeta.Defaults.Model, Source: SourceUserDefault}
	}
	return SettingExplanation{Value: oc.defaultModelForProvider(), Source: SourceProviderConfig}
}

func (oc *AIClient) getPromptWithSource(meta *PortalMetadata, loginMeta *UserLoginMetadata) SettingExplanation {
	if meta != nil && meta.SystemPrompt != "" {
		return SettingExplanation{Value: meta.SystemPrompt, Source: SourceRoomOverride}
	}
	if loginMeta.Defaults != nil && loginMeta.Defaults.SystemPrompt != "" {
		return SettingExplanation{Value: loginMeta.Defaults.SystemPrompt, Source: SourceUserDefault}
	}
	if oc.connector.Config.DefaultSystemPrompt != "" {
		return SettingExplanation{Value: oc.connector.Config.DefaultSystemPrompt, Source: SourceProviderConfig}
	}
	return SettingExplanation{Value: "", Source: SourceGlobalDefault}
}

func (oc *AIClient) getTempWithSource(meta *PortalMetadata, loginMeta *UserLoginMetadata) SettingExplanation {
	if meta != nil && meta.Temperature > 0 {
		return SettingExplanation{Value: meta.Temperature, Source: SourceRoomOverride}
	}
	if loginMeta.Defaults != nil && loginMeta.Defaults.Temperature != nil {
		return SettingExplanation{Value: *loginMeta.Defaults.Temperature, Source: SourceUserDefault}
	}
	return SettingExplanation{Value: nil, Source: SourceGlobalDefault, Reason: "provider/model default (unset)"}
}

func (oc *AIClient) getReasoningWithSource(meta *PortalMetadata, loginMeta *UserLoginMetadata) SettingExplanation {
	// Check model support first
	if meta != nil && !meta.Capabilities.SupportsReasoning {
		return SettingExplanation{Value: nil, Source: SourceModelLimit, Reason: "Model does not support reasoning"}
	}
	if meta != nil && meta.ReasoningEffort != "" {
		return SettingExplanation{Value: meta.ReasoningEffort, Source: SourceRoomOverride}
	}
	if loginMeta.Defaults != nil && loginMeta.Defaults.ReasoningEffort != "" {
		return SettingExplanation{Value: loginMeta.Defaults.ReasoningEffort, Source: SourceUserDefault}
	}
	if meta != nil && meta.Capabilities.SupportsReasoning {
		return SettingExplanation{Value: defaultReasoningEffort, Source: SourceGlobalDefault}
	}
	return SettingExplanation{Value: "", Source: SourceGlobalDefault}
}

// broadcastCapabilities sends bridge-controlled capabilities to Matrix room state
// This event is protected by power levels (100) so only the bridge bot can modify
func (oc *AIClient) broadcastCapabilities(ctx context.Context, portal *bridgev2.Portal) error {
	if portal.MXID == "" {
		return errors.New("portal has no Matrix room ID")
	}

	meta := portalMeta(portal)
	loginMeta := loginMetadata(oc.UserLogin)

	// Refresh stored model capabilities (room capabilities may add image-understanding union separately)
	modelCaps := oc.getModelCapabilitiesForMeta(meta)
	if meta.Capabilities != modelCaps {
		meta.Capabilities = modelCaps
		if err := portal.Save(ctx); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save portal after capability refresh")
		}
	}

	roomCaps := oc.getRoomCapabilities(ctx, meta)

	// Build reasoning effort options if model supports reasoning
	var reasoningEfforts []ReasoningEffortOption
	if roomCaps.SupportsReasoning {
		reasoningEfforts = []ReasoningEffortOption{
			{Value: "low", Label: "Low"},
			{Value: "medium", Label: "Medium"},
			{Value: "high", Label: "High"},
		}
	}

	content := &RoomCapabilitiesEventContent{
		Capabilities:           &roomCaps,
		AvailableTools:         oc.buildAvailableTools(meta),
		ReasoningEffortOptions: reasoningEfforts,
		Provider:               loginMeta.Provider,
		EffectiveSettings:      oc.buildEffectiveSettings(meta),
	}

	bot := oc.UserLogin.Bridge.Bot
	_, err := bot.SendState(ctx, portal.MXID, RoomCapabilitiesEventType, "", &event.Content{
		Parsed: content,
	}, time.Time{})

	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to broadcast room capabilities")
		return err
	}

	// Also update standard room features for clients
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)

	oc.loggerForContext(ctx).Debug().Str("model", meta.Model).Msg("Broadcasted room capabilities")
	return nil
}

// broadcastSettings sends user-editable settings to Matrix room state
// This event uses normal power levels (0) so users can modify
func (oc *AIClient) broadcastSettings(ctx context.Context, portal *bridgev2.Portal) error {
	if portal.MXID == "" {
		return errors.New("portal has no Matrix room ID")
	}

	meta := portalMeta(portal)

	content := &RoomSettingsEventContent{
		Model:               meta.Model,
		SystemPrompt:        meta.SystemPrompt,
		Temperature:         &meta.Temperature,
		MaxContextMessages:  meta.MaxContextMessages,
		MaxCompletionTokens: meta.MaxCompletionTokens,
		ReasoningEffort:     meta.ReasoningEffort,
		ConversationMode:    meta.ConversationMode,
		AgentID:             meta.AgentID,
	}

	bot := oc.UserLogin.Bridge.Bot
	_, err := bot.SendState(ctx, portal.MXID, RoomSettingsEventType, "", &event.Content{
		Parsed: content,
	}, time.Time{})

	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to broadcast room settings")
		return err
	}

	meta.LastRoomStateSync = time.Now().Unix()
	if err := portal.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save portal after state broadcast")
	}

	oc.loggerForContext(ctx).Debug().Str("model", meta.Model).Msg("Broadcasted room settings")
	return nil
}

// sendSystemNotice sends an informational notice to the room via the portal pipeline.
func (oc *AIClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if portal == nil || portal.MXID == "" {
		return
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     message,
				Mentions: &event.Mentions{},
			},
		}},
	}
	if _, _, err := oc.sendViaPortal(ctx, portal, converted, ""); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send system notice")
	}
}
