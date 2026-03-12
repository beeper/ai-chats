package store

import (
	"context"
	"database/sql"
	"strings"
)

type SessionRecord struct {
	SessionKey            string
	SessionID             string
	UpdatedAtMs           int64
	LastHeartbeatText     string
	LastHeartbeatSentAtMs int64
	LastChannel           string
	LastTo                string
	LastAccountID         string
	LastThreadID          string
	QueueMode             string
	QueueDebounceMs       *int
	QueueCap              *int
	QueueDrop             string
}

type SessionStore struct {
	scope *Scope
}

func (s *SessionStore) Get(ctx context.Context, sessionKey string) (SessionRecord, bool, error) {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return SessionRecord{}, false, nil
	}
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return SessionRecord{}, false, nil
	}
	var (
		record             SessionRecord
		queueDebounceMsRaw sql.NullInt64
		queueCapRaw        sql.NullInt64
	)
	err := s.scope.DB.QueryRow(ctx, `
		SELECT
			session_key, session_id, updated_at_ms, last_heartbeat_text,
			last_heartbeat_sent_at_ms, last_channel, last_to, last_account_id,
			last_thread_id, queue_mode, queue_debounce_ms, queue_cap, queue_drop
		FROM ai_sessions
		WHERE bridge_id=$1 AND login_id=$2 AND store_agent_id=$3 AND session_key=$4
	`, s.scope.BridgeID, s.scope.LoginID, normalizeAgentID(s.scope.AgentID), key).Scan(
		&record.SessionKey,
		&record.SessionID,
		&record.UpdatedAtMs,
		&record.LastHeartbeatText,
		&record.LastHeartbeatSentAtMs,
		&record.LastChannel,
		&record.LastTo,
		&record.LastAccountID,
		&record.LastThreadID,
		&record.QueueMode,
		&queueDebounceMsRaw,
		&queueCapRaw,
		&record.QueueDrop,
	)
	if err == sql.ErrNoRows {
		return SessionRecord{}, false, nil
	}
	if err != nil {
		return SessionRecord{}, false, err
	}
	record.QueueDebounceMs = nullableInt(queueDebounceMsRaw)
	record.QueueCap = nullableInt(queueCapRaw)
	return record, true, nil
}

func (s *SessionStore) Upsert(ctx context.Context, record SessionRecord) error {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return nil
	}
	key := strings.TrimSpace(record.SessionKey)
	if key == "" {
		return nil
	}
	_, err := s.scope.DB.Exec(ctx, `
		INSERT INTO ai_sessions (
			bridge_id, login_id, store_agent_id, session_key, session_id,
			updated_at_ms, last_heartbeat_text, last_heartbeat_sent_at_ms,
			last_channel, last_to, last_account_id, last_thread_id,
			queue_mode, queue_debounce_ms, queue_cap, queue_drop
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (bridge_id, login_id, store_agent_id, session_key) DO UPDATE SET
			session_id=excluded.session_id,
			updated_at_ms=excluded.updated_at_ms,
			last_heartbeat_text=excluded.last_heartbeat_text,
			last_heartbeat_sent_at_ms=excluded.last_heartbeat_sent_at_ms,
			last_channel=excluded.last_channel,
			last_to=excluded.last_to,
			last_account_id=excluded.last_account_id,
			last_thread_id=excluded.last_thread_id,
			queue_mode=excluded.queue_mode,
			queue_debounce_ms=excluded.queue_debounce_ms,
			queue_cap=excluded.queue_cap,
			queue_drop=excluded.queue_drop
	`, s.scope.BridgeID, s.scope.LoginID, normalizeAgentID(s.scope.AgentID), key,
		record.SessionID, record.UpdatedAtMs, record.LastHeartbeatText, record.LastHeartbeatSentAtMs,
		record.LastChannel, record.LastTo, record.LastAccountID, record.LastThreadID,
		record.QueueMode, nullableInt64Value(record.QueueDebounceMs), nullableInt64Value(record.QueueCap), record.QueueDrop,
	)
	return err
}

func normalizeAgentID(agentID string) string {
	if strings.TrimSpace(agentID) == "" {
		return "beep"
	}
	return strings.TrimSpace(agentID)
}

func nullableInt(raw sql.NullInt64) *int {
	if !raw.Valid {
		return nil
	}
	value := int(raw.Int64)
	return &value
}

func nullableInt64Value(value *int) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}
