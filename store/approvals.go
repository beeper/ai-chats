package store

import (
	"context"
	"strings"
	"time"
)

type ApprovalRecord struct {
	ApprovalID  string
	Kind        string
	RoomID      string
	TurnID      string
	ToolCallID  string
	ToolName    string
	RequestJSON string
	Status      string
	Reason      string
	ExpiresAtMs int64
	CreatedAtMs int64
	UpdatedAtMs int64
}

type ApprovalStore struct {
	scope *Scope
}

func (s *ApprovalStore) Upsert(ctx context.Context, record ApprovalRecord) error {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return nil
	}
	record.ApprovalID = strings.TrimSpace(record.ApprovalID)
	if record.ApprovalID == "" {
		return nil
	}
	now := time.Now().UnixMilli()
	if record.CreatedAtMs == 0 {
		record.CreatedAtMs = now
	}
	if record.UpdatedAtMs == 0 {
		record.UpdatedAtMs = now
	}
	_, err := s.scope.DB.Exec(ctx, `
		INSERT INTO ai_approvals (
			bridge_id, login_id, agent_id, approval_id, kind, room_id, turn_id,
			tool_call_id, tool_name, request_json, status, reason,
			expires_at_ms, created_at_ms, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (bridge_id, login_id, agent_id, approval_id) DO UPDATE SET
			kind=excluded.kind,
			room_id=excluded.room_id,
			turn_id=excluded.turn_id,
			tool_call_id=excluded.tool_call_id,
			tool_name=excluded.tool_name,
			request_json=excluded.request_json,
			status=excluded.status,
			reason=excluded.reason,
			expires_at_ms=excluded.expires_at_ms,
			updated_at_ms=excluded.updated_at_ms
	`, s.scope.BridgeID, s.scope.LoginID, normalizeAgentID(s.scope.AgentID), record.ApprovalID,
		record.Kind, record.RoomID, record.TurnID, record.ToolCallID, record.ToolName,
		record.RequestJSON, record.Status, record.Reason, record.ExpiresAtMs, record.CreatedAtMs, record.UpdatedAtMs,
	)
	return err
}

func (s *ApprovalStore) Get(ctx context.Context, approvalID string) (ApprovalRecord, bool, error) {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return ApprovalRecord{}, false, nil
	}
	record := ApprovalRecord{}
	err := s.scope.DB.QueryRow(ctx, `
		SELECT approval_id, kind, room_id, turn_id, tool_call_id, tool_name,
		       request_json, status, reason, expires_at_ms, created_at_ms, updated_at_ms
		FROM ai_approvals
		WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3 AND approval_id=$4
	`, s.scope.BridgeID, s.scope.LoginID, normalizeAgentID(s.scope.AgentID), strings.TrimSpace(approvalID)).Scan(
		&record.ApprovalID, &record.Kind, &record.RoomID, &record.TurnID,
		&record.ToolCallID, &record.ToolName, &record.RequestJSON, &record.Status,
		&record.Reason, &record.ExpiresAtMs, &record.CreatedAtMs, &record.UpdatedAtMs,
	)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return ApprovalRecord{}, false, nil
		}
		return ApprovalRecord{}, false, err
	}
	return record, true, nil
}
