package sdk

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

type PortalLifecycleOptions struct {
	Login                *bridgev2.UserLogin
	Portal               *bridgev2.Portal
	ChatInfo             *bridgev2.ChatInfo
	SaveBeforeCreate     bool
	CleanupOnCreateError func(context.Context, *bridgev2.Portal)
	AIRoomKind           string
	ForceCapabilities    bool
	RefreshExtra         func(context.Context, *bridgev2.Portal)
}

// EnsurePortalLifecycle creates or refreshes a portal room and then applies
// the shared room-state lifecycle used across bridge implementations.
func EnsurePortalLifecycle(ctx context.Context, opts PortalLifecycleOptions) (bool, error) {
	if opts.Portal == nil {
		return false, fmt.Errorf("missing portal")
	}
	if opts.Login == nil {
		return false, fmt.Errorf("missing login")
	}
	if opts.SaveBeforeCreate {
		if err := opts.Portal.Save(ctx); err != nil {
			return false, fmt.Errorf("failed to save portal: %w", err)
		}
	}

	created := opts.Portal.MXID == ""
	if created {
		if err := opts.Portal.CreateMatrixRoom(ctx, opts.Login, opts.ChatInfo); err != nil {
			if opts.CleanupOnCreateError != nil {
				opts.CleanupOnCreateError(ctx, opts.Portal)
			}
			return false, err
		}
	} else if opts.ChatInfo != nil {
		opts.Portal.UpdateInfo(ctx, opts.ChatInfo, opts.Login, nil, time.Time{})
	}

	RefreshPortalLifecycle(ctx, opts)
	return created, nil
}

// RefreshPortalLifecycle applies explicit room-state refresh steps that are
// expected after room creation, room refresh, or portal re-ID.
func RefreshPortalLifecycle(ctx context.Context, opts PortalLifecycleOptions) {
	if opts.Portal == nil || opts.Portal.MXID == "" {
		return
	}
	opts.Portal.UpdateBridgeInfo(ctx)
	if opts.ForceCapabilities && opts.Login != nil {
		opts.Portal.UpdateCapabilities(ctx, opts.Login, true)
	}
	if opts.AIRoomKind != "" {
		agentremote.SendAIRoomInfo(ctx, opts.Portal, opts.AIRoomKind)
	}
	if opts.RefreshExtra != nil {
		opts.RefreshExtra(ctx, opts.Portal)
	}
}
