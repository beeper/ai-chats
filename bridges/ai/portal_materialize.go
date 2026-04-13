package ai

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type portalRoomMaterializeOptions struct {
	SaveBefore           bool
	CleanupOnCreateError string
	SendWelcome          bool
	MutatePortal         func(*bridgev2.Portal)
	BeforeSave           func(context.Context, *bridgev2.Portal) error
	OnCreated            func(context.Context, *bridgev2.Portal) error
	OnExisting           func(context.Context, *bridgev2.Portal) error
}

func (oc *AIClient) materializePortalRoom(
	ctx context.Context,
	portal *bridgev2.Portal,
	chatInfo *bridgev2.ChatInfo,
	opts portalRoomMaterializeOptions,
) error {
	if portal == nil {
		return fmt.Errorf("missing portal")
	}
	if oc == nil || oc.UserLogin == nil {
		return fmt.Errorf("AIClient not initialized: missing UserLogin")
	}
	if opts.MutatePortal != nil {
		opts.MutatePortal(portal)
	}
	if opts.BeforeSave != nil {
		if err := opts.BeforeSave(ctx, portal); err != nil {
			return err
		}
	}
	created, err := sdk.EnsurePortalLifecycle(ctx, sdk.PortalLifecycleOptions{
		Login:            oc.UserLogin,
		Portal:           portal,
		ChatInfo:         chatInfo,
		SaveBeforeCreate: opts.SaveBefore,
		CleanupOnCreateError: func(ctx context.Context, portal *bridgev2.Portal) {
			if opts.CleanupOnCreateError != "" {
				cleanupPortal(ctx, oc, portal, opts.CleanupOnCreateError)
			}
		},
		AIRoomKind:        integrationPortalAIKind(portalMeta(portal)),
		ForceCapabilities: true,
	})
	if err != nil {
		return err
	}
	if created {
		if opts.SendWelcome {
			if err := oc.sendWelcomeMessage(ctx, portal); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to send welcome message")
			}
		}
		if opts.OnCreated != nil {
			return opts.OnCreated(ctx, portal)
		}
	} else if opts.OnExisting != nil {
		return opts.OnExisting(ctx, portal)
	}
	return nil
}
