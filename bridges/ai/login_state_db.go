package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

type loginRuntimeState struct {
	NextChatIndex      int
	LastHeartbeatEvent *HeartbeatEventPayload
}

type loginStateScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func loginStateScopeForClient(client *AIClient) *loginStateScope {
	db, bridgeID, loginID := loginDBContext(client)
	if db == nil || strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return nil
	}
	return &loginStateScope{
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
	}
}

func cloneHeartbeatEvent(in *HeartbeatEventPayload) *HeartbeatEventPayload {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func cloneLoginRuntimeState(in *loginRuntimeState) *loginRuntimeState {
	if in == nil {
		return &loginRuntimeState{}
	}
	return &loginRuntimeState{
		NextChatIndex:      in.NextChatIndex,
		LastHeartbeatEvent: cloneHeartbeatEvent(in.LastHeartbeatEvent),
	}
}

func parseHeartbeatEvent(raw string) (*HeartbeatEventPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var evt HeartbeatEventPayload
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return nil, err
	}
	return &evt, nil
}

func marshalJSONOrEmpty(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(data) == "null" {
		return "", nil
	}
	return string(data), nil
}

func loadLoginRuntimeState(ctx context.Context, client *AIClient) (*loginRuntimeState, error) {
	scope := loginStateScopeForClient(client)
	if scope == nil {
		return &loginRuntimeState{}, nil
	}
	state := &loginRuntimeState{}
	var lastHeartbeatEventJSON string
	err := scope.db.QueryRow(ctx, `
		SELECT next_chat_index, last_heartbeat_event_json
		FROM `+aiLoginStateTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID).Scan(
		&state.NextChatIndex,
		&lastHeartbeatEventJSON,
	)
	if err == sql.ErrNoRows {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	state.LastHeartbeatEvent, err = parseHeartbeatEvent(lastHeartbeatEventJSON)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func saveLoginRuntimeState(ctx context.Context, client *AIClient, state *loginRuntimeState) error {
	scope := loginStateScopeForClient(client)
	if scope == nil || state == nil {
		return nil
	}
	lastHeartbeatEventJSON, err := marshalJSONOrEmpty(state.LastHeartbeatEvent)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiLoginStateTable+` (
			bridge_id, login_id, next_chat_index, last_heartbeat_event_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id) DO UPDATE SET
			next_chat_index=excluded.next_chat_index,
			last_heartbeat_event_json=excluded.last_heartbeat_event_json,
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.bridgeID, scope.loginID, state.NextChatIndex, lastHeartbeatEventJSON, time.Now().UnixMilli(),
	)
	return err
}

func (oc *AIClient) ensureLoginStateLoaded(ctx context.Context) *loginRuntimeState {
	oc.loginStateMu.Lock()
	defer oc.loginStateMu.Unlock()
	if oc.loginState != nil {
		return oc.loginState
	}
	state, err := loadLoginRuntimeState(ctx, oc)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load AI login runtime state")
		state = &loginRuntimeState{}
	}
	oc.loginState = state
	return oc.loginState
}

func (oc *AIClient) loginStateSnapshot(ctx context.Context) *loginRuntimeState {
	return cloneLoginRuntimeState(oc.ensureLoginStateLoaded(ctx))
}

func (oc *AIClient) updateLoginState(ctx context.Context, fn func(*loginRuntimeState) bool) error {
	if oc == nil {
		return nil
	}
	oc.loginStateMu.Lock()
	defer oc.loginStateMu.Unlock()
	if oc.loginState == nil {
		state, err := loadLoginRuntimeState(ctx, oc)
		if err != nil {
			return err
		}
		oc.loginState = state
	}
	if !fn(oc.loginState) {
		return nil
	}
	return saveLoginRuntimeState(ctx, oc, oc.loginState)
}

func (oc *AIClient) clearLoginState(ctx context.Context) {
	scope := loginStateScopeForClient(oc)
	if scope != nil {
		bestEffortExec(ctx, scope.db, oc.Log(),
			`DELETE FROM `+aiLoginStateTable+` WHERE bridge_id=$1 AND login_id=$2`,
			scope.bridgeID, scope.loginID,
		)
	}
	oc.loginStateMu.Lock()
	oc.loginState = &loginRuntimeState{}
	oc.loginStateMu.Unlock()
}
