package ai

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

type loginRuntimeState struct {
	NextChatIndex         int
	DefaultChatPortalID   string
	ToolApprovals         *ToolApprovalsConfig
	LastActiveRoomByAgent map[string]string
	LastHeartbeatEvent    *HeartbeatEventPayload
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
		NextChatIndex:         in.NextChatIndex,
		DefaultChatPortalID:   in.DefaultChatPortalID,
		ToolApprovals:         cloneToolApprovalsConfig(in.ToolApprovals),
		LastActiveRoomByAgent: cloneStringMap(in.LastActiveRoomByAgent),
		LastHeartbeatEvent:    cloneHeartbeatEvent(in.LastHeartbeatEvent),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneToolApprovalsConfig(in *ToolApprovalsConfig) *ToolApprovalsConfig {
	if in == nil {
		return nil
	}
	copy := *in
	copy.MCPAlwaysAllow = append([]MCPAlwaysAllowRule(nil), in.MCPAlwaysAllow...)
	copy.BuiltinAlwaysAllow = append([]BuiltinAlwaysAllowRule(nil), in.BuiltinAlwaysAllow...)
	return &copy
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

func parseToolApprovals(raw string) (*ToolApprovalsConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var cfg ToolApprovalsConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func parseStringMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
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
	var defaultChatPortalID, toolApprovalsJSON, lastActiveRoomByAgentJSON, lastHeartbeatEventJSON string
	err := scope.db.QueryRow(ctx, `
		SELECT next_chat_index, default_chat_portal_id, tool_approvals_json, last_active_room_by_agent_json, last_heartbeat_event_json
		FROM aichats_login_state
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID).Scan(
		&state.NextChatIndex,
		&defaultChatPortalID,
		&toolApprovalsJSON,
		&lastActiveRoomByAgentJSON,
		&lastHeartbeatEventJSON,
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no rows") {
			return state, nil
		}
		return nil, err
	}
	var parseErr error
	state.DefaultChatPortalID = strings.TrimSpace(defaultChatPortalID)
	state.ToolApprovals, parseErr = parseToolApprovals(toolApprovalsJSON)
	if parseErr != nil {
		return nil, parseErr
	}
	state.LastActiveRoomByAgent, parseErr = parseStringMap(lastActiveRoomByAgentJSON)
	if parseErr != nil {
		return nil, parseErr
	}
	state.LastHeartbeatEvent, parseErr = parseHeartbeatEvent(lastHeartbeatEventJSON)
	if parseErr != nil {
		return nil, parseErr
	}
	return state, nil
}

func saveLoginRuntimeState(ctx context.Context, client *AIClient, state *loginRuntimeState) error {
	scope := loginStateScopeForClient(client)
	if scope == nil || state == nil {
		return nil
	}
	toolApprovalsJSON, err := marshalJSONOrEmpty(state.ToolApprovals)
	if err != nil {
		return err
	}
	lastActiveRoomByAgentJSON, err := marshalJSONOrEmpty(state.LastActiveRoomByAgent)
	if err != nil {
		return err
	}
	lastHeartbeatEventJSON, err := marshalJSONOrEmpty(state.LastHeartbeatEvent)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO aichats_login_state (
			bridge_id, login_id, next_chat_index, default_chat_portal_id, tool_approvals_json,
			last_active_room_by_agent_json, last_heartbeat_event_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bridge_id, login_id) DO UPDATE SET
			next_chat_index=excluded.next_chat_index,
			default_chat_portal_id=excluded.default_chat_portal_id,
			tool_approvals_json=excluded.tool_approvals_json,
			last_active_room_by_agent_json=excluded.last_active_room_by_agent_json,
			last_heartbeat_event_json=excluded.last_heartbeat_event_json,
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.bridgeID, scope.loginID, state.NextChatIndex, strings.TrimSpace(state.DefaultChatPortalID), toolApprovalsJSON,
		lastActiveRoomByAgentJSON, lastHeartbeatEventJSON, time.Now().UnixMilli(),
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
			`DELETE FROM aichats_login_state WHERE bridge_id=$1 AND login_id=$2`,
			scope.bridgeID, scope.loginID,
		)
	}
	oc.loginStateMu.Lock()
	oc.loginState = &loginRuntimeState{}
	oc.loginStateMu.Unlock()
}
