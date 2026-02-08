package connector

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

func (oc *AIClient) toolApprovalsAskFallback() string {
	if oc == nil || oc.connector == nil {
		return "deny"
	}
	cfg := oc.connector.Config.ToolApprovals.WithDefaults()
	switch strings.ToLower(strings.TrimSpace(cfg.AskFallback)) {
	case "allow":
		return "allow"
	default:
		return "deny"
	}
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
	meta := loginMetadata(oc.UserLogin)
	cfg := meta.ToolApprovals
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
	meta := loginMetadata(oc.UserLogin)
	cfg := meta.ToolApprovals
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

func (oc *AIClient) persistAlwaysAllow(ctx context.Context, pending *pendingToolApproval) error {
	if oc == nil || oc.UserLogin == nil || pending == nil {
		return nil
	}
	meta := loginMetadata(oc.UserLogin)
	if meta.ToolApprovals == nil {
		meta.ToolApprovals = &ToolApprovalsConfig{}
	}

	switch pending.ToolKind {
	case ToolApprovalKindMCP:
		sl := normalizeApprovalToken(pending.ServerLabel)
		tn := normalizeMcpRuleToolName(pending.RuleToolName)
		if sl == "" || tn == "" {
			return nil
		}
		for _, rule := range meta.ToolApprovals.MCPAlwaysAllow {
			if normalizeApprovalToken(rule.ServerLabel) == sl && normalizeMcpRuleToolName(rule.ToolName) == tn {
				return nil
			}
		}
		meta.ToolApprovals.MCPAlwaysAllow = append(meta.ToolApprovals.MCPAlwaysAllow, MCPAlwaysAllowRule{
			ServerLabel: sl,
			ToolName:    tn,
		})
	case ToolApprovalKindBuiltin:
		tn := normalizeApprovalToken(pending.RuleToolName)
		act := normalizeApprovalToken(pending.Action)
		if tn == "" {
			return nil
		}
		for _, rule := range meta.ToolApprovals.BuiltinAlwaysAllow {
			if normalizeApprovalToken(rule.ToolName) == tn && normalizeApprovalToken(rule.Action) == act {
				return nil
			}
		}
		meta.ToolApprovals.BuiltinAlwaysAllow = append(meta.ToolApprovals.BuiltinAlwaysAllow, BuiltinAlwaysAllowRule{
			ToolName: tn,
			Action:   act,
		})
	default:
		return nil
	}

	return oc.UserLogin.Save(ctx)
}
