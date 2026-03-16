package agentremote

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

// ReactionTarget provides the bridge-specific context that BaseReactionHandler
// needs to validate and route approval reactions.
type ReactionTarget interface {
	GetUserLogin() *bridgev2.UserLogin
	GetApprovalHandler() ApprovalReactionHandler
}

// BaseReactionHandler is an embeddable mixin that implements the three reaction
// interface methods (PreHandleMatrixReaction, HandleMatrixReaction,
// HandleMatrixReactionRemove) for bridges whose reaction handling is limited to
// approval prompt reactions.
type BaseReactionHandler struct {
	Target ReactionTarget
}

func (h BaseReactionHandler) PreHandleMatrixReaction(_ context.Context, msg *bridgev2.MatrixReaction) (bridgev2.MatrixReactionPreResponse, error) {
	return PreHandleApprovalReaction(msg)
}

func (h BaseReactionHandler) HandleMatrixReaction(ctx context.Context, msg *bridgev2.MatrixReaction) (*database.Reaction, error) {
	if h.Target == nil || msg == nil || msg.Event == nil || msg.Portal == nil {
		return &database.Reaction{}, nil
	}
	login := h.Target.GetUserLogin()
	if login != nil && IsMatrixBotUser(ctx, login.Bridge, msg.Event.Sender) {
		return &database.Reaction{}, nil
	}
	// Best-effort persistence guard for reaction.sender_id -> ghost.id FK.
	if err := EnsureSyntheticReactionSenderGhost(ctx, login, msg.Event.Sender); err != nil {
		logger := loggerForLogin(ctx, login)
		logEvt := logger.Warn().Err(err).Stringer("sender_mxid", msg.Event.Sender)
		if login != nil {
			logEvt = logEvt.Str("user_login_id", string(login.ID))
		}
		logEvt.Msg("Failed to ensure synthetic Matrix reaction sender ghost")
	}
	rc := ExtractReactionContext(msg)
	if handler := h.Target.GetApprovalHandler(); handler != nil {
		handler.HandleReaction(ctx, msg, rc.TargetEventID, rc.Emoji)
	}
	return &database.Reaction{}, nil
}

func (h BaseReactionHandler) HandleMatrixReactionRemove(ctx context.Context, msg *bridgev2.MatrixReactionRemove) error {
	if h.Target == nil || msg == nil {
		return nil
	}
	approvalHandler := h.Target.GetApprovalHandler()
	if approvalHandler == nil {
		return nil
	}
	if handler, ok := approvalHandler.(ApprovalReactionRemoveHandler); ok {
		handler.HandleReactionRemove(ctx, msg)
	}
	return nil
}
