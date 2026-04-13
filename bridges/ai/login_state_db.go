package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type loginRuntimeState struct {
	NextChatIndex       int
	LastHeartbeatEvent  *HeartbeatEventPayload
	ModelCache          *ModelCache
	FileAnnotationCache map[string]FileAnnotation
	ConsecutiveErrors   int
	LastErrorAt         int64
}

func (state *loginRuntimeState) UpdateHeartbeat(evt *HeartbeatEventPayload) bool {
	if state == nil || evt == nil {
		return false
	}
	if prev := state.LastHeartbeatEvent; prev != nil {
		if prev.TS == evt.TS && prev.Status == evt.Status && prev.Reason == evt.Reason && prev.To == evt.To && prev.Channel == evt.Channel && prev.Preview == evt.Preview {
			return false
		}
	}
	state.LastHeartbeatEvent = cloneHeartbeatEvent(evt)
	return true
}

func (state *loginRuntimeState) RecordProviderError(now time.Time, warningThreshold int) (int, bool) {
	if state == nil {
		return 0, false
	}
	prevErrors := state.ConsecutiveErrors
	state.ConsecutiveErrors++
	state.LastErrorAt = now.Unix()
	return state.ConsecutiveErrors, prevErrors < warningThreshold && state.ConsecutiveErrors >= warningThreshold
}

func (state *loginRuntimeState) RecordProviderSuccess(warningThreshold int) bool {
	if state == nil || state.ConsecutiveErrors == 0 {
		return false
	}
	recovered := state.ConsecutiveErrors >= warningThreshold
	state.ConsecutiveErrors = 0
	state.LastErrorAt = 0
	return recovered
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
		NextChatIndex:       in.NextChatIndex,
		LastHeartbeatEvent:  cloneHeartbeatEvent(in.LastHeartbeatEvent),
		ModelCache:          cloneModelCache(in.ModelCache),
		FileAnnotationCache: cloneFileAnnotationCache(in.FileAnnotationCache),
		ConsecutiveErrors:   in.ConsecutiveErrors,
		LastErrorAt:         in.LastErrorAt,
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
	scope := loginScopeForClient(client)
	if scope == nil {
		return &loginRuntimeState{}, nil
	}
	state := &loginRuntimeState{}
	var (
		lastHeartbeatEventJSON string
		modelCacheJSON         string
		fileAnnotationJSON     string
	)
	err := scope.db.QueryRow(ctx, `
		SELECT
			next_chat_index,
			last_heartbeat_event_json,
			model_cache_json,
			file_annotation_cache_json,
			consecutive_errors,
			last_error_at
		FROM `+aiLoginStateTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID).Scan(
		&state.NextChatIndex,
		&lastHeartbeatEventJSON,
		&modelCacheJSON,
		&fileAnnotationJSON,
		&state.ConsecutiveErrors,
		&state.LastErrorAt,
	)
	if err == sql.ErrNoRows {
		return &loginRuntimeState{}, nil
	}
	if err != nil {
		return nil, err
	}
	state.LastHeartbeatEvent, err = parseHeartbeatEvent(lastHeartbeatEventJSON)
	if err != nil {
		return nil, err
	}
	if state.ModelCache, err = unmarshalJSONField[ModelCache](modelCacheJSON); err != nil {
		return nil, err
	}
	if state.FileAnnotationCache, err = unmarshalMapJSONField[string, FileAnnotation](fileAnnotationJSON); err != nil {
		return nil, err
	}
	return state, nil
}

func saveLoginRuntimeState(ctx context.Context, client *AIClient, state *loginRuntimeState) error {
	scope := loginScopeForClient(client)
	if scope == nil || state == nil {
		return nil
	}
	lastHeartbeatEventJSON, err := marshalJSONOrEmpty(state.LastHeartbeatEvent)
	if err != nil {
		return err
	}
	modelCacheJSON, err := marshalJSONOrEmpty(state.ModelCache)
	if err != nil {
		return err
	}
	fileAnnotationJSON, err := marshalJSONOrEmpty(state.FileAnnotationCache)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiLoginStateTable+` (
			bridge_id,
			login_id,
			next_chat_index,
			last_heartbeat_event_json,
			model_cache_json,
			file_annotation_cache_json,
			consecutive_errors,
			last_error_at,
			updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (bridge_id, login_id) DO UPDATE SET
			next_chat_index=excluded.next_chat_index,
			last_heartbeat_event_json=excluded.last_heartbeat_event_json,
			model_cache_json=excluded.model_cache_json,
			file_annotation_cache_json=excluded.file_annotation_cache_json,
			consecutive_errors=excluded.consecutive_errors,
			last_error_at=excluded.last_error_at,
			updated_at_ms=excluded.updated_at_ms
	`,
		scope.bridgeID,
		scope.loginID,
		state.NextChatIndex,
		lastHeartbeatEventJSON,
		modelCacheJSON,
		fileAnnotationJSON,
		state.ConsecutiveErrors,
		state.LastErrorAt,
		time.Now().UnixMilli(),
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
		return &loginRuntimeState{}
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
	nextState := cloneLoginRuntimeState(oc.loginState)
	if !fn(nextState) {
		return nil
	}
	if err := saveLoginRuntimeState(ctx, oc, nextState); err != nil {
		return err
	}
	oc.loginState = nextState
	return nil
}

func (oc *AIClient) clearLoginState(ctx context.Context) {
	scope := loginScopeForClient(oc)
	if scope != nil {
		execDelete(ctx, scope.db, oc.Log(),
			`DELETE FROM `+aiLoginStateTable+` WHERE bridge_id=$1 AND login_id=$2`,
			scope.bridgeID, scope.loginID,
		)
	}
	oc.loginStateMu.Lock()
	oc.loginState = &loginRuntimeState{}
	oc.loginStateMu.Unlock()
}
