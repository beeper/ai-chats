package ai

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) HandleMatrixMembership(ctx context.Context, msg *bridgev2.MatrixMembershipChange) (*bridgev2.MatrixMembershipResult, error) {
	if msg == nil || msg.Portal == nil || msg.Type != bridgev2.Invite {
		return nil, bridgev2.ErrMembershipNotSupported
	}
	ghost, ok := msg.Target.(*bridgev2.Ghost)
	if !ok || ghost == nil {
		return nil, bridgev2.ErrMembershipNotSupported
	}
	target, err := oc.resolveChatTargetFromGhost(ctx, ghost)
	if err != nil {
		return nil, err
	}
	if target == nil || target.modelID == "" {
		return nil, bridgev2.ErrMembershipNotSupported
	}
	canonicalGhost := modelUserID(target.modelID)
	if ghost.ID != canonicalGhost {
		return &bridgev2.MatrixMembershipResult{RedirectTo: canonicalGhost}, nil
	}
	return nil, oc.switchPortalModel(ctx, msg.Portal, target.modelID)
}

func (oc *AIClient) switchPortalModel(ctx context.Context, portal *bridgev2.Portal, modelID string) error {
	if portal == nil {
		return bridgev2.ErrMembershipNotSupported
	}
	meta := portalMeta(portal)
	setPortalResolvedTarget(portal, meta, modelUserID(modelID))
	if err := oc.savePortal(ctx, portal, "model membership change"); err != nil {
		return err
	}
	oc.ensureGhostDisplayName(ctx, modelID)
	if portal.MXID != "" {
		portal.UpdateInfo(ctx, oc.chatInfoFromPortal(ctx, portal), oc.UserLogin, nil, time.Time{})
		portal.UpdateBridgeInfo(ctx)
		portal.UpdateCapabilities(ctx, oc.UserLogin, true)
		oc.sendAIChatsRoomInfo(ctx, portal)
	}
	return nil
}
