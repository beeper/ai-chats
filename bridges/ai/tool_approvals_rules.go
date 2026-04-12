package ai

import (
	"context"
	"strings"
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

func (oc *AIClient) isMcpAlwaysAllowed(serverLabel, toolName string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	state := oc.loginStateSnapshot(context.Background())
	cfg := state.ToolApprovals
	if cfg == nil || len(cfg.MCPAlwaysAllow) == 0 {
		return false
	}
	sl := normalizeApprovalToken(serverLabel)
	tn := normalizeMcpRuleToolName(toolName)
	if sl == "" || tn == "" {
		return false
	}
	for _, rule := range cfg.MCPAlwaysAllow {
		if normalizeApprovalToken(rule.ServerLabel) == sl && normalizeMcpRuleToolName(rule.ToolName) == tn {
			return true
		}
	}
	return false
}

func (oc *AIClient) isBuiltinAlwaysAllowed(toolName, action string) bool {
	if oc == nil || oc.UserLogin == nil {
		return false
	}
	state := oc.loginStateSnapshot(context.Background())
	cfg := state.ToolApprovals
	if cfg == nil || len(cfg.BuiltinAlwaysAllow) == 0 {
		return false
	}
	tn := normalizeApprovalToken(toolName)
	act := normalizeApprovalToken(action)
	if tn == "" {
		return false
	}
	for _, rule := range cfg.BuiltinAlwaysAllow {
		if normalizeApprovalToken(rule.ToolName) != tn {
			continue
		}
		rAct := normalizeApprovalToken(rule.Action)
		if rAct == "" || rAct == act {
			return true
		}
	}
	return false
}

func (oc *AIClient) persistAlwaysAllow(ctx context.Context, pending *pendingToolApprovalData) error {
	if oc == nil || oc.UserLogin == nil || pending == nil {
		return nil
	}
	return oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		if state.ToolApprovals == nil {
			state.ToolApprovals = &ToolApprovalsConfig{}
		}
		switch pending.ToolKind {
		case ToolApprovalKindMCP:
			sl := normalizeApprovalToken(pending.ServerLabel)
			tn := normalizeMcpRuleToolName(pending.RuleToolName)
			if sl == "" || tn == "" {
				return false
			}
			for _, rule := range state.ToolApprovals.MCPAlwaysAllow {
				if normalizeApprovalToken(rule.ServerLabel) == sl && normalizeMcpRuleToolName(rule.ToolName) == tn {
					return false
				}
			}
			state.ToolApprovals.MCPAlwaysAllow = append(state.ToolApprovals.MCPAlwaysAllow, MCPAlwaysAllowRule{
				ServerLabel: sl,
				ToolName:    tn,
			})
			return true
		case ToolApprovalKindBuiltin:
			tn := normalizeApprovalToken(pending.RuleToolName)
			act := normalizeApprovalToken(pending.Action)
			if tn == "" {
				return false
			}
			for _, rule := range state.ToolApprovals.BuiltinAlwaysAllow {
				if normalizeApprovalToken(rule.ToolName) == tn && normalizeApprovalToken(rule.Action) == act {
					return false
				}
			}
			state.ToolApprovals.BuiltinAlwaysAllow = append(state.ToolApprovals.BuiltinAlwaysAllow, BuiltinAlwaysAllowRule{
				ToolName: tn,
				Action:   act,
			})
			return true
		default:
			return false
		}
	})
}
