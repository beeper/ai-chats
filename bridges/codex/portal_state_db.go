package codex

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/aidb"
)

const codexPortalStateTable = "codex_portal_state"

var codexPortalStateBlob = aidb.JSONBlobTable{
	TableName: codexPortalStateTable,
	KeyColumn: "portal_key",
}

type codexPortalState struct {
	Title            string `json:"title,omitempty"`
	Slug             string `json:"slug,omitempty"`
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	CodexCwd         string `json:"codex_cwd,omitempty"`
	ElevatedLevel    string `json:"elevated_level,omitempty"`
	AwaitingCwdSetup bool   `json:"awaiting_cwd_setup,omitempty"`
	ManagedImport    bool   `json:"managed_import,omitempty"`
}

type codexPersistedPortalState struct {
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	CodexCwd         string `json:"codex_cwd,omitempty"`
	ElevatedLevel    string `json:"elevated_level,omitempty"`
	AwaitingCwdSetup bool   `json:"awaiting_cwd_setup,omitempty"`
	ManagedImport    bool   `json:"managed_import,omitempty"`
}

type codexPortalStateRecord struct {
	PortalKey networkid.PortalKey
	State     *codexPortalState
}

func codexPortalBlobScope(portal *bridgev2.Portal) *aidb.BlobScope {
	if portal == nil {
		return nil
	}
	return aidb.PortalBlobScope(portal, &codexPortalStateBlob, portal.PortalKey.String())
}

func codexLoginBlobScope(login *bridgev2.UserLogin) *aidb.BlobScope {
	return aidb.LoginBlobScope(login, &codexPortalStateBlob, "")
}

func loadCodexPortalState(ctx context.Context, portal *bridgev2.Portal) (*codexPortalState, error) {
	persisted, err := aidb.LoadScopedOrNew[codexPersistedPortalState](ctx, codexPortalBlobScope(portal))
	if err != nil {
		return nil, err
	}
	state := &codexPortalState{}
	if persisted != nil {
		state.CodexThreadID = persisted.CodexThreadID
		state.CodexCwd = persisted.CodexCwd
		state.ElevatedLevel = persisted.ElevatedLevel
		state.AwaitingCwdSetup = persisted.AwaitingCwdSetup
		state.ManagedImport = persisted.ManagedImport
	}
	if meta := portalMeta(portal); meta != nil {
		state.Title = strings.TrimSpace(meta.Title)
		state.Slug = strings.TrimSpace(meta.Slug)
	}
	return state, nil
}

func saveCodexPortalState(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState) error {
	if portal != nil {
		if meta := portalMeta(portal); meta != nil {
			meta.Title = strings.TrimSpace(state.Title)
			meta.Slug = strings.TrimSpace(state.Slug)
			if err := portal.Save(ctx); err != nil {
				return err
			}
		}
	}
	return aidb.SaveScoped(ctx, codexPortalBlobScope(portal), &codexPersistedPortalState{
		CodexThreadID:    strings.TrimSpace(state.CodexThreadID),
		CodexCwd:         strings.TrimSpace(state.CodexCwd),
		ElevatedLevel:    strings.TrimSpace(state.ElevatedLevel),
		AwaitingCwdSetup: state.AwaitingCwdSetup,
		ManagedImport:    state.ManagedImport,
	})
}

func clearCodexPortalState(ctx context.Context, portal *bridgev2.Portal) error {
	return aidb.DeleteScoped(ctx, codexPortalBlobScope(portal))
}

func listCodexPortalStateRecords(ctx context.Context, login *bridgev2.UserLogin) ([]codexPortalStateRecord, error) {
	scope := codexLoginBlobScope(login)
	if scope == nil {
		return nil, nil
	}
	if err := codexPortalStateBlob.Ensure(ctx, scope.DB); err != nil {
		return nil, err
	}
	rows, err := scope.DB.Query(ctx, `
		SELECT portal_key, state_json
		FROM `+codexPortalStateTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.BridgeID, scope.LoginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []codexPortalStateRecord
	for rows.Next() {
		var portalKeyRaw, stateRaw string
		if err := rows.Scan(&portalKeyRaw, &stateRaw); err != nil {
			return nil, err
		}
		key, ok := parseCodexPortalKey(portalKeyRaw)
		if !ok {
			continue
		}
		state := &codexPortalState{}
		if strings.TrimSpace(stateRaw) != "" {
			var persisted codexPersistedPortalState
			if err := json.Unmarshal([]byte(stateRaw), &persisted); err != nil {
				zerolog.Ctx(ctx).Warn().Err(err).Str("portal_key", portalKeyRaw).Msg("skipping malformed codex portal state")
				continue
			}
			state.CodexThreadID = persisted.CodexThreadID
			state.CodexCwd = persisted.CodexCwd
			state.ElevatedLevel = persisted.ElevatedLevel
			state.AwaitingCwdSetup = persisted.AwaitingCwdSetup
			state.ManagedImport = persisted.ManagedImport
		}
		out = append(out, codexPortalStateRecord{
			PortalKey: key,
			State:     state,
		})
	}
	return out, rows.Err()
}

func parseCodexPortalKey(raw string) (networkid.PortalKey, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return networkid.PortalKey{}, false
	}
	id, receiver, ok := strings.Cut(raw, "/")
	if !ok {
		return networkid.PortalKey{ID: networkid.PortalID(raw)}, true
	}
	key := networkid.PortalKey{ID: networkid.PortalID(id)}
	if strings.TrimSpace(receiver) != "" {
		key.Receiver = networkid.UserLoginID(receiver)
	}
	return key, true
}
