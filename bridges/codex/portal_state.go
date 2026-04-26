package codex

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type codexPortalState struct {
	Title            string `json:"title,omitempty"`
	Slug             string `json:"slug,omitempty"`
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	CodexCwd         string `json:"codex_cwd,omitempty"`
	ElevatedLevel    string `json:"elevated_level,omitempty"`
	AwaitingCwdSetup bool   `json:"awaiting_cwd_setup,omitempty"`
	ManagedImport    bool   `json:"managed_import,omitempty"`
}

type codexPortalStateRecord struct {
	PortalKey networkid.PortalKey
	Portal    *bridgev2.Portal
	State     *codexPortalState
}

func loadCodexPortalState(_ context.Context, portal *bridgev2.Portal) (*codexPortalState, error) {
	if portal == nil {
		return nil, nil
	}
	meta := portalMeta(portal)
	return &codexPortalState{
		Title:            strings.TrimSpace(meta.Title),
		Slug:             strings.TrimSpace(meta.Slug),
		CodexThreadID:    strings.TrimSpace(meta.CodexThreadID),
		CodexCwd:         strings.TrimSpace(meta.CodexCwd),
		ElevatedLevel:    strings.TrimSpace(meta.ElevatedLevel),
		AwaitingCwdSetup: meta.AwaitingCwdSetup,
		ManagedImport:    meta.ManagedImport,
	}, nil
}

func saveCodexPortalState(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState) error {
	if portal == nil || state == nil {
		return nil
	}
	meta := portalMeta(portal)
	meta.Title = strings.TrimSpace(state.Title)
	meta.Slug = strings.TrimSpace(state.Slug)
	meta.CodexThreadID = strings.TrimSpace(state.CodexThreadID)
	meta.CodexCwd = strings.TrimSpace(state.CodexCwd)
	meta.ElevatedLevel = strings.TrimSpace(state.ElevatedLevel)
	meta.AwaitingCwdSetup = state.AwaitingCwdSetup
	meta.ManagedImport = state.ManagedImport
	return portal.Save(ctx)
}

func clearCodexPortalState(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil {
		return nil
	}
	meta := portalMeta(portal)
	meta.IsCodexRoom = false
	meta.Title = ""
	meta.Slug = ""
	meta.CodexThreadID = ""
	meta.CodexCwd = ""
	meta.ElevatedLevel = ""
	meta.AwaitingCwdSetup = false
	meta.ManagedImport = false
	return portal.Save(ctx)
}

func listCodexPortalStateRecords(ctx context.Context, login *bridgev2.UserLogin) ([]codexPortalStateRecord, error) {
	if login == nil || login.Bridge == nil {
		return nil, nil
	}
	portals, err := login.Bridge.GetAllPortals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]codexPortalStateRecord, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.Receiver != login.ID {
			continue
		}
		meta := portalMeta(portal)
		if meta == nil || !meta.IsCodexRoom {
			continue
		}
		state, err := loadCodexPortalState(ctx, portal)
		if err != nil {
			return nil, err
		}
		out = append(out, codexPortalStateRecord{
			PortalKey: portal.PortalKey,
			Portal:    portal,
			State:     state,
		})
	}
	return out, nil
}
