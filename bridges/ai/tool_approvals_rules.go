package ai

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func normalizeApprovalToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeMcpRuleToolName(name string) string {
	n := normalizeApprovalToken(name)
	return strings.TrimPrefix(n, "mcp.")
}

func (oc *AIClient) toolApprovalsRuntimeEnabled() bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	return cfg.Enabled != nil && *cfg.Enabled
}

func (oc *AIClient) toolApprovalsTTLSeconds() int {
	if oc == nil || oc.connector == nil {
		return 600
	}
	return oc.connector.Config.ToolApprovals.WithDefaults().TTLSeconds
}

func (oc *AIClient) toolApprovalsRequireForMCP() bool {
	if oc == nil || oc.connector == nil {
		return true
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	return cfg.RequireForMCP == nil || *cfg.RequireForMCP
}

func (oc *AIClient) toolApprovalsRequireForTool(toolName string) bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	if cfg.RequireForTools == nil {
		return false
	}
	needle := normalizeApprovalToken(toolName)
	for _, raw := range cfg.RequireForTools {
		if normalizeApprovalToken(raw) == needle {
			return true
		}
	}
	return false
}

func (oc *AIClient) isMcpAlwaysAllowed(ctx context.Context, serverLabel, toolName string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	sl := normalizeApprovalToken(serverLabel)
	tn := normalizeMcpRuleToolName(toolName)
	if sl == "" || tn == "" {
		return false
	}
	return oc.hasToolApprovalRule(ctx, ToolApprovalKindMCP, sl, tn, "")
}

func (oc *AIClient) isBuiltinAlwaysAllowed(ctx context.Context, toolName, action string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	tn := normalizeApprovalToken(toolName)
	act := normalizeApprovalToken(action)
	if tn == "" {
		return false
	}
	return oc.hasBuiltinToolApprovalRule(ctx, tn, act)
}

func (oc *AIClient) persistAlwaysAllow(ctx context.Context, pending *pendingToolApprovalData) error {
	if oc == nil || oc.UserLogin == nil || pending == nil {
		return nil
	}
	switch pending.ToolKind {
	case ToolApprovalKindMCP:
		sl := normalizeApprovalToken(pending.ServerLabel)
		tn := normalizeMcpRuleToolName(pending.RuleToolName)
		if sl == "" || tn == "" {
			return nil
		}
		return oc.insertToolApprovalRule(ctx, ToolApprovalKindMCP, sl, tn, "")
	case ToolApprovalKindBuiltin:
		tn := normalizeApprovalToken(pending.RuleToolName)
		act := normalizeApprovalToken(pending.Action)
		if tn == "" {
			return nil
		}
		return oc.insertToolApprovalRule(ctx, ToolApprovalKindBuiltin, "", tn, act)
	default:
		return nil
	}
}

func (oc *AIClient) hasToolApprovalRule(ctx context.Context, toolKind ToolApprovalKind, serverLabel, toolName, action string) bool {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return false
	}
	var matched int
	err := scope.db.QueryRow(ctx, `
		SELECT 1
		FROM aichats_tool_approval_rules
		WHERE bridge_id=$1 AND login_id=$2 AND tool_kind=$3 AND server_label=$4 AND tool_name=$5 AND action=$6
		LIMIT 1
	`, scope.bridgeID, scope.loginID, string(toolKind), serverLabel, toolName, action).Scan(&matched)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("tool_kind", string(toolKind)).Str("tool_name", toolName).Msg("tool approvals: lookup failed")
		return false
	}
	return matched == 1
}

func (oc *AIClient) hasBuiltinToolApprovalRule(ctx context.Context, toolName, action string) bool {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return false
	}
	var matched int
	err := scope.db.QueryRow(ctx, `
		SELECT 1
		FROM aichats_tool_approval_rules
		WHERE bridge_id=$1 AND login_id=$2 AND tool_kind=$3 AND server_label='' AND tool_name=$4 AND (action='' OR action=$5)
		LIMIT 1
	`, scope.bridgeID, scope.loginID, string(ToolApprovalKindBuiltin), toolName, action).Scan(&matched)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		oc.Log().Warn().Err(err).Str("tool_name", toolName).Str("action", action).Msg("tool approvals: builtin lookup failed")
		return false
	}
	return matched == 1
}

func (oc *AIClient) insertToolApprovalRule(ctx context.Context, toolKind ToolApprovalKind, serverLabel, toolName, action string) error {
	scope := loginScopeForClient(oc)
	if scope == nil {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		INSERT INTO aichats_tool_approval_rules (
			bridge_id, login_id, tool_kind, server_label, tool_name, action, created_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bridge_id, login_id, tool_kind, server_label, tool_name, action) DO NOTHING
	`, scope.bridgeID, scope.loginID, string(toolKind), serverLabel, toolName, action, time.Now().UnixMilli())
	return err
}
