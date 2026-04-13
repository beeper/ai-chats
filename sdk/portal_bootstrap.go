package sdk

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type DMPortalBootstrapSpec struct {
	Login                *bridgev2.UserLogin
	Portal               *bridgev2.Portal
	PortalKey            networkid.PortalKey
	Title                string
	Topic                string
	OtherUserID          networkid.UserID
	PortalMutate         func(*bridgev2.Portal)
	BeforeSave           func(context.Context, *bridgev2.Portal) error
	ChatInfo             *bridgev2.ChatInfo
	CreateRoomIfMissing  bool
	SaveBeforeCreate     bool
	CleanupOnCreateError func(context.Context, *bridgev2.Portal)
	AIRoomKind           string
	ForceCapabilities    bool
}

type DMPortalBootstrapResult struct {
	Portal   *bridgev2.Portal
	ChatInfo *bridgev2.ChatInfo
	Created  bool
}

func BootstrapDMPortal(ctx context.Context, spec DMPortalBootstrapSpec) (*DMPortalBootstrapResult, error) {
	if spec.Login == nil || spec.Login.Bridge == nil {
		return nil, fmt.Errorf("login unavailable")
	}
	portal := spec.Portal
	if portal == nil {
		if spec.PortalKey == (networkid.PortalKey{}) {
			return nil, fmt.Errorf("missing portal")
		}
		var err error
		portal, err = spec.Login.Bridge.GetPortalByKey(ctx, spec.PortalKey)
		if err != nil {
			return nil, err
		}
	}
	if portal == nil {
		return nil, fmt.Errorf("missing portal")
	}

	if err := ConfigureDMPortal(ctx, ConfigureDMPortalParams{
		Portal:      portal,
		Title:       spec.Title,
		Topic:       spec.Topic,
		OtherUserID: spec.OtherUserID,
		Save:        false,
		MutatePortal: func(portal *bridgev2.Portal) {
			if spec.PortalMutate != nil {
				spec.PortalMutate(portal)
			}
		},
	}); err != nil {
		return nil, err
	}
	if spec.BeforeSave != nil {
		if err := spec.BeforeSave(ctx, portal); err != nil {
			return nil, err
		}
	}

	if !spec.CreateRoomIfMissing {
		if err := portal.Save(ctx); err != nil {
			return nil, fmt.Errorf("failed to save portal: %w", err)
		}
		return &DMPortalBootstrapResult{
			Portal:   portal,
			ChatInfo: spec.ChatInfo,
		}, nil
	}

	created, err := EnsurePortalLifecycle(ctx, PortalLifecycleOptions{
		Login:                spec.Login,
		Portal:               portal,
		ChatInfo:             spec.ChatInfo,
		SaveBeforeCreate:     spec.SaveBeforeCreate,
		CleanupOnCreateError: spec.CleanupOnCreateError,
		AIRoomKind:           spec.AIRoomKind,
		ForceCapabilities:    spec.ForceCapabilities,
	})
	if err != nil {
		return nil, err
	}
	return &DMPortalBootstrapResult{
		Portal:   portal,
		ChatInfo: spec.ChatInfo,
		Created:  created,
	}, nil
}
