package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const codexPortalStateTable = "codex_portal_state"

type codexPortalState struct {
	Title            string `json:"title,omitempty"`
	Slug             string `json:"slug,omitempty"`
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	CodexCwd         string `json:"codex_cwd,omitempty"`
	ElevatedLevel    string `json:"elevated_level,omitempty"`
	AwaitingCwdSetup bool   `json:"awaiting_cwd_setup,omitempty"`
	ManagedImport    bool   `json:"managed_import,omitempty"`
}

type codexPortalStateScope struct {
	db        *dbutil.Database
	bridgeID  string
	loginID   string
	portalKey string
}

type codexPortalStateRecord struct {
	PortalKey networkid.PortalKey
	State     *codexPortalState
}

func codexPortalStateScopeForPortal(portal *bridgev2.Portal) *codexPortalStateScope {
	if portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil || portal.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(portal.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(portal.Receiver))
	portalKey := strings.TrimSpace(portal.PortalKey.String())
	if bridgeID == "" || loginID == "" || portalKey == "" {
		return nil
	}
	return &codexPortalStateScope{
		db:        portal.Bridge.DB.Database,
		bridgeID:  bridgeID,
		loginID:   loginID,
		portalKey: portalKey,
	}
}

func codexPortalStateScopeForLogin(login *bridgev2.UserLogin) *codexPortalStateScope {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil || login.Bridge.DB.Database == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &codexPortalStateScope{
		db:       login.Bridge.DB.Database,
		bridgeID: bridgeID,
		loginID:  loginID,
	}
}

func ensureCodexPortalStateTable(ctx context.Context, portal *bridgev2.Portal) error {
	scope := codexPortalStateScopeForPortal(portal)
	if scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := scope.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+codexPortalStateTable+` (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			portal_key TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '{}',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id, portal_key)
		)
	`)
	return err
}

func loadCodexPortalState(ctx context.Context, portal *bridgev2.Portal) (*codexPortalState, error) {
	scope := codexPortalStateScopeForPortal(portal)
	if scope == nil {
		return &codexPortalState{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureCodexPortalStateTable(ctx, portal); err != nil {
		return nil, err
	}
	var raw string
	err := scope.db.QueryRow(ctx, `
		SELECT state_json
		FROM `+codexPortalStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_key=$3
	`, scope.bridgeID, scope.loginID, scope.portalKey).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return &codexPortalState{}, nil
	}
	if err != nil {
		return nil, err
	}
	state := &codexPortalState{}
	if err := json.Unmarshal([]byte(raw), state); err != nil {
		return nil, err
	}
	return state, nil
}

func saveCodexPortalState(ctx context.Context, portal *bridgev2.Portal, state *codexPortalState) error {
	scope := codexPortalStateScopeForPortal(portal)
	if scope == nil || state == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureCodexPortalStateTable(ctx, portal); err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+codexPortalStateTable+` (
			bridge_id, login_id, portal_key, state_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, portal_key) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, scope.bridgeID, scope.loginID, scope.portalKey, string(payload), time.Now().UnixMilli())
	return err
}

func clearCodexPortalState(ctx context.Context, portal *bridgev2.Portal) error {
	scope := codexPortalStateScopeForPortal(portal)
	if scope == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureCodexPortalStateTable(ctx, portal); err != nil {
		return err
	}
	_, err := scope.db.Exec(ctx, `
		DELETE FROM `+codexPortalStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_key=$3
	`, scope.bridgeID, scope.loginID, scope.portalKey)
	return err
}

func listCodexPortalStateRecords(ctx context.Context, login *bridgev2.UserLogin) ([]codexPortalStateRecord, error) {
	scope := codexPortalStateScopeForLogin(login)
	if scope == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := scope.db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+codexPortalStateTable+` (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			portal_key TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '{}',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id, portal_key)
		)
	`)
	if err != nil {
		return nil, err
	}
	rows, err := scope.db.Query(ctx, `
		SELECT portal_key, state_json
		FROM `+codexPortalStateTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID)
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
				return nil, err
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
