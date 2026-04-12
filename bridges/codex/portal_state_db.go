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

type codexPortalStateRecord struct {
	PortalKey networkid.PortalKey
	State     *codexPortalState
}

func codexPortalBlobScope(portal *bridgev2.Portal) *aidb.BlobScope {
	if portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil || portal.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(portal.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(portal.Receiver))
	portalKey := strings.TrimSpace(portal.PortalKey.String())
	if bridgeID == "" || loginID == "" || portalKey == "" {
		return nil
	}
	return &aidb.BlobScope{
		Table:    &codexPortalStateBlob,
		DB:       portal.Bridge.DB.Database,
		BridgeID: bridgeID,
		LoginID:  loginID,
		Key:      portalKey,
	}
}

func codexLoginBlobScope(login *bridgev2.UserLogin) *aidb.BlobScope {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &aidb.BlobScope{
		Table:    &codexPortalStateBlob,
		DB:       login.Bridge.DB.Database,
		BridgeID: bridgeID,
		LoginID:  loginID,
	}
}

func loadCodexPortalState(ctx context.Context, portal *bridgev2.Portal) (*codexPortalState, error) {
	return aidb.LoadScopedOrNew[codexPortalState](ctx, codexPortalBlobScope(portal))
}

func saveCodexPortalState(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState) error {
	return aidb.SaveScoped(ctx, codexPortalBlobScope(portal), state)
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
			if err := json.Unmarshal([]byte(stateRaw), state); err != nil {
				zerolog.Ctx(ctx).Warn().Err(err).Str("portal_key", portalKeyRaw).Msg("skipping malformed codex portal state")
				continue
			}
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
