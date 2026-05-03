package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/beeper/ai-chats/pkg/shared/aihelpers"
	"github.com/beeper/ai-chats/pkg/shared/bridgeutil"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func (oc *AIClient) modelJoinMember(ctx context.Context, loginID networkid.UserLoginID, modelID, modelName string, info *ModelInfo) bridgev2.ChatMember {
	responder, err := oc.resolveResponder(ctx, &PortalMetadata{
		ResolvedTarget: &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			ModelID: modelID,
		},
	}, ResponderResolveOptions{})
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("model", modelID).Msg("Failed to resolve responder for model join member")
	}
	memberExtra := responderMetadataMap(responder)
	if memberExtra == nil {
		memberExtra = map[string]any{}
	}
	memberExtra["displayname"] = modelName
	userInfo := responderUserInfoOrDefault(responder, modelContactName(modelID, info), modelContactIdentifiers(modelID), false)
	return bridgev2.ChatMember{
		EventSender: bridgev2.EventSender{
			Sender:      modelUserID(modelID),
			SenderLogin: loginID,
		},
		Membership:       event.MembershipJoin,
		UserInfo:         userInfo,
		MemberEventExtra: memberExtra,
	}
}

type chatCreateParams struct {
	ModelID            string
	ApplyModelOverride bool
	Title              string
	PortalKey          *networkid.PortalKey
	RoomName           string
	ParentRoomID       id.RoomID
	SkipRoomCreation   bool
}

func (oc *AIClient) createChat(ctx context.Context, params chatCreateParams) (*bridgev2.CreateChatResponse, error) {
	modelID := strings.TrimSpace(params.ModelID)
	initOpts := PortalInitOpts{
		ModelID:   modelID,
		Title:     strings.TrimSpace(params.Title),
		PortalKey: params.PortalKey,
	}
	portal, chatInfo, err := oc.initPortalForChat(ctx, initOpts)
	if err != nil {
		return nil, err
	}
	roomName := strings.TrimSpace(params.RoomName)
	if roomName != "" {
		portal.Name = roomName
		portal.NameSet = true
		if chatInfo != nil {
			chatInfo.Name = &roomName
		}
	}
	if roomName != "" {
		if err := oc.savePortal(ctx, portal, "chat setup"); err != nil {
			return nil, fmt.Errorf("failed to save chat setup: %w", err)
		}
	}
	if !params.SkipRoomCreation {
		portal, err = oc.ensurePortalRoom(ctx, ensurePortalRoomParams{
			Portal:   portal,
			ChatInfo: chatInfo,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to materialize chat room: %w", err)
		}
		if err := oc.sendDisclaimerNotice(ctx, portal); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Stringer("portal", portal.PortalKey).Msg("Failed to send initial disclaimer after chat creation")
		}
	}

	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		Portal:     portal,
		PortalInfo: chatInfo,
	}, nil
}

// allocateNextChatIndex increments and returns the next chat index for this login
func (oc *AIClient) allocateNextChatIndex(ctx context.Context) (int, error) {
	oc.chatLock.Lock()
	defer oc.chatLock.Unlock()

	var next int
	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		state.NextChatIndex++
		next = state.NextChatIndex
		return true
	}); err != nil {
		return 0, fmt.Errorf("failed to save login state: %w", err)
	}
	return next, nil
}

// PortalInitOpts contains options for initializing a chat portal
type PortalInitOpts struct {
	ModelID   string
	Title     string
	CopyFrom  *PortalMetadata // For forked chats - copies config from source
	PortalKey *networkid.PortalKey
}

func cloneForkPortalMetadata(src *PortalMetadata, slug, title string) *PortalMetadata {
	if src == nil {
		return nil
	}
	clone := &PortalMetadata{
		Slug: slug,
	}
	if src.ResolvedTarget != nil {
		target := *src.ResolvedTarget
		clone.ResolvedTarget = &target
	}
	return clone
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
	var pmeta *PortalMetadata
	if opts.CopyFrom != nil {
		pmeta = cloneForkPortalMetadata(opts.CopyFrom, slug, title)
	} else {
		pmeta = &PortalMetadata{
			Slug: slug,
		}
	}
	chatInfo := oc.composeChatInfo(ctx, title, modelID)
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to bootstrap portal: %w", err)
	}
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = modelUserID(modelID)
	portal.Name = strings.TrimSpace(title)
	portal.NameSet = portal.Name != ""
	portal.Topic = ""
	portal.TopicSet = false
	portal.Metadata = pmeta
	setPortalResolvedTarget(portal, pmeta, modelUserID(modelID))
	if err := oc.savePortal(ctx, portal, "chat bootstrap"); err != nil {
		return nil, nil, fmt.Errorf("failed to bootstrap portal: %w", err)
	}
	oc.ensureGhostDisplayName(ctx, modelID)
	return portal, chatInfo, nil
}

// handleNewChat creates a new chat using the current room's model.

// chatInfoFromPortal builds ChatInfo from an existing portal
func (oc *AIClient) chatInfoFromPortal(ctx context.Context, portal *bridgev2.Portal) *bridgev2.ChatInfo {
	if portal == nil {
		return nil
	}
	meta := portalMeta(portal)
	if meta != nil && meta.InternalRoom() {
		fallbackName := strings.TrimSpace(meta.Slug)
		if fallbackName == "" {
			fallbackName = "AI Chat"
		}
		name := strings.TrimSpace(portal.Name)
		if name == "" {
			name = fallbackName
		}
		return &bridgev2.ChatInfo{
			Name:  ptr.Ptr(name),
			Topic: ptr.NonZero(strings.TrimSpace(portal.Topic)),
		}
	}
	modelID := oc.effectiveModel(meta)
	title := strings.TrimSpace(portal.Name)
	if title == "" {
		if meta != nil && strings.TrimSpace(meta.Slug) != "" {
			slug := strings.TrimSpace(meta.Slug)
			title = slug
		} else {
			title = modelContactName(modelID, oc.findModelInfo(modelID))
		}
	}
	chatInfo := oc.composeChatInfo(ctx, title, modelID)
	return chatInfo
}

// composeChatInfo creates a ChatInfo struct for a chat
func (oc *AIClient) composeChatInfo(ctx context.Context, title, modelID string) *bridgev2.ChatInfo {
	if modelID == "" {
		modelID = oc.effectiveModel(nil)
	}
	modelInfo := oc.findModelInfo(modelID)
	modelName := modelContactName(modelID, modelInfo)
	if title == "" {
		title = modelName
	}
	chatInfo := bridgeutil.BuildDMChatInfo(bridgeutil.DMChatInfoParams{
		Title:          title,
		Topic:          "",
		HumanUserID:    humanUserID(oc.UserLogin.ID),
		LoginID:        oc.UserLogin.ID,
		BotUserID:      modelUserID(modelID),
		BotDisplayName: modelName,
		CanBackfill:    false,
	})
	// Override bot member with model-specific UserInfo and extra fields.
	chatInfo.Members.MemberMap[modelUserID(modelID)] = oc.modelJoinMember(ctx, oc.UserLogin.ID, modelID, modelName, modelInfo)
	return chatInfo
}

func (oc *AIClient) sendSystemNoticeMessage(ctx context.Context, portal *bridgev2.Portal, message string) error {
	if oc == nil || oc.UserLogin == nil || portal == nil {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	portal, err := resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return err
	}
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	return aihelpers.SendSystemMessage(ctx, oc.UserLogin, portal, oc.senderForPortal(ctx, portal), message)
}

// sendSystemNotice sends a bridge-authored notice via the shared AIHelper transport
// path instead of maintaining a bridge-local Matrix send implementation.
func (oc *AIClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, message string) {
	if err := oc.sendSystemNoticeMessage(ctx, portal, message); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send system notice")
	}
}

// HandleMatrixMessageRemove keeps bridgev2 and the AI turn store in sync.
func (oc *AIClient) HandleMatrixMessageRemove(ctx context.Context, msg *bridgev2.MatrixMessageRemove) error {
	if oc == nil || msg == nil || msg.Portal == nil || msg.TargetMessage == nil {
		return nil
	}
	oc.loggerForContext(ctx).Debug().
		Stringer("event_id", msg.TargetMessage.MXID).
		Stringer("portal", msg.Portal.PortalKey).
		Msg("Handling message deletion")

	var errs []error
	if err := oc.deleteAITurnByExternalRef(ctx, msg.Portal, msg.TargetMessage.ID, msg.TargetMessage.MXID); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// HandleMatrixDisappearingTimer handles disappearing message timer changes from Matrix
// For AI Chats, update only the portal disappear field; the bridge framework handles deletion.
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
