package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/agents/tools"
	"github.com/beeper/ai-bridge/pkg/shared/toolspec"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// Tool name constants
const (
	ToolNameCalculator = toolspec.CalculatorName
	ToolNameWebSearch  = toolspec.WebSearchName
)

// defaultSimpleModeSystemPrompt is the default system prompt for simple mode rooms.
const defaultSimpleModeSystemPrompt = "You are a helpful assistant."

var ErrDMGhostImmutable = errors.New("can't change the counterpart ghost in a DM")

func hasAssignedAgent(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	return meta.AgentID != ""
}

func hasBossAgent(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	return agents.IsBossAgent(meta.AgentID)
}

func dmModelSwitchGuidance(targetModel string) string {
	if strings.TrimSpace(targetModel) == "" {
		return "This is a DM. Switching to a different model requires creating a new chat."
	}
	return fmt.Sprintf("This is a DM. Switching to %s requires creating a new chat (for example: `!ai simple new %s`).", targetModel, targetModel)
}

func dmModelSwitchBlockedError(targetModel string) error {
	return fmt.Errorf("%w: %s", ErrDMGhostImmutable, dmModelSwitchGuidance(targetModel))
}

func modelRedirectTarget(requested, resolved string) networkid.UserID {
	requested = strings.TrimSpace(requested)
	resolved = strings.TrimSpace(resolved)
	if requested == "" || resolved == "" || requested == resolved {
		return ""
	}
	return modelUserID(resolved)
}

// validateDMModelSwitch enforces the DM invariant that counterpart ghosts are immutable.
// Agent rooms are exempt because the stable counterpart ghost is the agent ghost.
func (oc *AIClient) validateDMModelSwitch(portal *bridgev2.Portal, meta *PortalMetadata, targetModel string) error {
	if oc == nil || portal == nil || meta == nil || strings.TrimSpace(targetModel) == "" {
		return nil
	}
	if portal.RoomType != database.RoomTypeDM {
		return nil
	}
	if resolveAgentID(meta) != "" {
		return nil
	}
	currentModel := oc.effectiveModel(meta)
	if currentModel == "" || currentModel == targetModel {
		return nil
	}
	currentGhost := modelUserID(currentModel)
	targetGhost := modelUserID(targetModel)
	if currentGhost == targetGhost {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrDMGhostImmutable, currentModel, targetModel)
}

// buildAvailableTools returns a list of ToolInfo for all tools based on tool policy.
func (oc *AIClient) buildAvailableTools(meta *PortalMetadata) []ToolInfo {
	names := oc.toolNamesForPortal(meta)
	var toolsList []ToolInfo

	for _, name := range names {
		metaTool := tools.GetTool(name)
		displayName := name
		description := ""
		toolType := "builtin"
		if metaTool != nil {
			description = metaTool.Description
			if metaTool.Annotations != nil && metaTool.Annotations.Title != "" {
				displayName = metaTool.Annotations.Title
			}
			if metaTool.Type != "" {
				toolType = string(metaTool.Type)
			}
		} else if oc != nil {
			lookupCtx, cancel := context.WithTimeout(context.Background(), mcpDiscoveryTimeout)
			if dynamicTool, ok := oc.lookupMCPToolDefinition(lookupCtx, name); ok {
				description = dynamicTool.Description
				toolType = string(ToolTypeMCP)
			}
			cancel()
		}
		description = oc.toolDescriptionForPortal(meta, name, description)

		available, source, reason := oc.isToolAvailable(meta, name)
		allowed := oc.isToolAllowedByPolicy(meta, name)
		enabled := available && allowed

		if !allowed {
			source = SourceAgentPolicy
			if reason == "" {
				reason = "Disabled by tool policy"
			}
		}

		toolsList = append(toolsList, ToolInfo{
			Name:        name,
			DisplayName: displayName,
			Description: description,
			Type:        toolType,
			Enabled:     enabled,
			Available:   available,
			Source:      source,
			Reason:      reason,
		})
	}

	return toolsList
}

func (oc *AIClient) canUseImageGeneration() bool {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Metadata == nil {
		return false
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil || loginMeta.APIKey == "" {
		return false
	}
	switch loginMeta.Provider {
	case ProviderOpenAI, ProviderOpenRouter, ProviderBeeper, ProviderMagicProxy:
		return true
	default:
		return false
	}
}

// allocateNextChatIndex increments and returns the next chat index for this login
func (oc *AIClient) allocateNextChatIndex(ctx context.Context) (int, error) {
	meta := loginMetadata(oc.UserLogin)
	oc.chatLock.Lock()
	defer oc.chatLock.Unlock()

	meta.NextChatIndex++
	if err := oc.UserLogin.Save(ctx); err != nil {
		meta.NextChatIndex-- // Rollback on error
		return 0, fmt.Errorf("failed to save login: %w", err)
	}

	return meta.NextChatIndex, nil
}

// PortalInitOpts contains options for initializing a chat portal
type PortalInitOpts struct {
	ModelID      string
	Title        string
	SystemPrompt string
	CopyFrom     *PortalMetadata // For forked chats - copies config from source
	PortalKey    *networkid.PortalKey
}

func cloneForkPortalMetadata(src *PortalMetadata, slug, title string) *PortalMetadata {
	if src == nil {
		return nil
	}
	return &PortalMetadata{
		Model:               src.Model,
		Slug:                slug,
		Title:               title,
		SystemPrompt:        src.SystemPrompt,
		Temperature:         src.Temperature,
		MaxContextMessages:  src.MaxContextMessages,
		MaxCompletionTokens: src.MaxCompletionTokens,
		ReasoningEffort:     src.ReasoningEffort,
		Capabilities:        src.Capabilities,
		ConversationMode:    src.ConversationMode,
		AgentID:             src.AgentID,
		AgentPrompt:         src.AgentPrompt,
		IsSimpleMode:        src.IsSimpleMode,
	}
}

// initPortalForChat handles common portal initialization logic.
// Returns the configured portal, chat info, and any error.
func (oc *AIClient) initPortalForChat(ctx context.Context, opts PortalInitOpts) (*bridgev2.Portal, *bridgev2.ChatInfo, error) {
	chatIndex, err := oc.allocateNextChatIndex(ctx)
	if err != nil {
		return nil, nil, err
	}

	slug := formatChatSlug(chatIndex)
	modelID := opts.ModelID
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}

	title := opts.Title
	if title == "" {
		modelName := modelContactName(modelID, oc.findModelInfo(modelID))
		title = fmt.Sprintf("AI Chat with %s", modelName)
	}

	portalKey := portalKeyForChat(oc.UserLogin.ID)
	if opts.PortalKey != nil {
		portalKey = *opts.PortalKey
	}
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get portal: %w", err)
	}

	// Initialize or copy metadata
	var pmeta *PortalMetadata
	if opts.CopyFrom != nil {
		pmeta = cloneForkPortalMetadata(opts.CopyFrom, slug, title)
		modelID = opts.CopyFrom.Model
	} else {
		pmeta = &PortalMetadata{
			Model:        modelID,
			Slug:         slug,
			Title:        title,
			SystemPrompt: opts.SystemPrompt,
			Capabilities: getModelCapabilities(modelID, oc.findModelInfo(modelID)),
		}
	}
	portal.Metadata = pmeta

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = modelUserID(modelID)
	portal.Name = title
	portal.NameSet = true
	defaultAvatar := strings.TrimSpace(agents.DefaultAgentAvatarMXC)
	if defaultAvatar != "" {
		portal.AvatarID = networkid.AvatarID(defaultAvatar)
		portal.AvatarMXC = id.ContentURIString(defaultAvatar)
	}
	// Note: portal.Topic is NOT set to SystemPrompt - they are separate concepts

	if err := portal.Save(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to save portal: %w", err)
	}
	oc.ensureGhostDisplayName(ctx, modelID)

	chatInfo := oc.composeChatInfo(title, modelID)
	return portal, chatInfo, nil
}

// updatePortalConfig applies room settings to portal metadata with optimistic updates.
// If persistence fails, metadata is rolled back to the previous values.
func (oc *AIClient) updatePortalConfig(ctx context.Context, portal *bridgev2.Portal, config *RoomSettingsEventContent) error {
	meta := portalMeta(portal)
	before := clonePortalMetadata(meta)

	// Track old model for membership change
	oldModel := meta.Model

	if config.Model != "" {
		if err := oc.validateDMModelSwitch(portal, meta, config.Model); err != nil {
			return dmModelSwitchBlockedError(config.Model)
		}
	}

	// Update only non-empty/non-zero values
	if config.Model != "" {
		meta.Model = config.Model
		// Update capabilities when model changes
		meta.Capabilities = getModelCapabilities(config.Model, oc.findModelInfo(config.Model))
	}
	if config.SystemPrompt != "" {
		meta.SystemPrompt = config.SystemPrompt
	}
	if config.Temperature != nil {
		meta.Temperature = *config.Temperature
	}
	if config.MaxContextMessages > 0 {
		meta.MaxContextMessages = config.MaxContextMessages
	}
	if config.MaxCompletionTokens > 0 {
		meta.MaxCompletionTokens = config.MaxCompletionTokens
	}
	if config.ReasoningEffort != "" {
		meta.ReasoningEffort = config.ReasoningEffort
	}
	if config.ConversationMode != "" {
		meta.ConversationMode = config.ConversationMode
	}
	if config.AgentID != "" {
		meta.AgentID = config.AgentID
	}

	meta.LastRoomStateSync = time.Now().Unix()

	// Persist changes
	if err := portal.Save(ctx); err != nil {
		if before != nil {
			*meta = *before
		}
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save portal after config update")
		return err
	}

	// Re-broadcast room state to confirm changes to all clients
	if err := oc.BroadcastRoomState(ctx, portal); err != nil {
		if before != nil {
			*meta = *before
			if saveErr := portal.Save(ctx); saveErr != nil {
				oc.loggerForContext(ctx).Warn().Err(saveErr).Msg("Failed to save rollback portal metadata after state broadcast failure")
			}
		}
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to re-broadcast room state after config update")
		return err
	}

	// Handle model switch - generate membership events if model changed.
	// This is done after persistence succeeds so optimistic updates can roll back safely.
	if config.Model != "" && oldModel != "" && config.Model != oldModel {
		oc.handleModelSwitch(ctx, portal, oldModel, config.Model)
	}

	return nil
}

// HandleMatrixMessageRemove handles message deletions from Matrix
// For AI bridge, we just delete from our database - there's no "remote" to sync to
func (oc *AIClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	oc.loggerForContext(ctx).Debug().
		Stringer("event_id", msg.TargetMessage.MXID).
		Stringer("portal", msg.Portal.PortalKey).
		Msg("Handling message deletion")

	// Delete from our database - the Matrix side is already handled by the bridge framework
	if err := oc.UserLogin.Bridge.DB.Message.Delete(ctx, msg.TargetMessage.RowID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Stringer("event_id", msg.TargetMessage.MXID).Msg("Failed to delete message from database")
		return err
	}
	oc.notifySessionMutation(ctx, msg.Portal, portalMeta(msg.Portal), true)

	return nil
}

// HandleMatrixDisappearingTimer handles disappearing message timer changes from Matrix
// For AI bridge, we just update the portal's disappear field - the bridge framework handles the actual deletion
func (oc *AIClient) HandleMatrixDisappearingTimer(ctx context.Context, msg *bridgev2.MatrixDisappearingTimer) (bool, error) {
	oc.loggerForContext(ctx).Debug().
		Stringer("portal", msg.Portal.PortalKey).
		Str("type", string(msg.Content.Type)).
		Dur("timer", msg.Content.Timer.Duration).
		Msg("Handling disappearing timer change")

	// Convert event to database setting and update portal
	setting := database.DisappearingSettingFromEvent(msg.Content)
	changed := msg.Portal.UpdateDisappearingSetting(ctx, setting, bridgev2.UpdateDisappearingSettingOpts{
		Save: true,
	})

	return changed, nil
}
