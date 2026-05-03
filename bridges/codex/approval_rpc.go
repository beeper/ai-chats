package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/beeper/agentremote/bridges/codex/codexrpc"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

type codexApprovalRequestParams struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	ItemID     string `json:"itemId"`
	ApprovalID string `json:"approvalId"`
}

type codexApprovalBehavior struct {
	AllowSession         bool
	RequestedPermissions map[string]any
}

func codexApprovalID(req codexrpc.Request, explicit string) string {
	if id := strings.TrimSpace(explicit); id != "" {
		return id
	}
	return strings.Trim(strings.TrimSpace(string(req.ID)), "\"")
}

func codexApprovalResponseValue(approved, always bool, reason string, allowSession bool) string {
	if approved {
		if allowSession && always {
			return "acceptForSession"
		}
		return "accept"
	}
	switch strings.TrimSpace(reason) {
	case sdk.ApprovalReasonCancelled, sdk.ApprovalReasonTimeout, sdk.ApprovalReasonExpired, sdk.ApprovalReasonDeliveryError:
		return "cancel"
	default:
		return "decline"
	}
}

func codexSessionApprovalDetails(details []sdk.ApprovalDetail) []sdk.ApprovalDetail {
	return append(details, sdk.ApprovalDetail{
		Label: "Session approval",
		Value: "Choosing Always allow grants permission for this Codex session only.",
	})
}

func codexAppendPermissionDetails(details []sdk.ApprovalDetail, permissions map[string]any) []sdk.ApprovalDetail {
	if network, ok := permissions["network"].(map[string]any); ok {
		details = sdk.AppendDetailsFromMap(details, "Network", network, 4)
	}
	if fileSystem, ok := permissions["fileSystem"].(map[string]any); ok {
		details = sdk.AppendDetailsFromMap(details, "File system", fileSystem, 4)
	}
	if macos, ok := permissions["macos"].(map[string]any); ok {
		details = sdk.AppendDetailsFromMap(details, "macOS", macos, 4)
	}
	return details
}

// resolveApprovalForActiveTurn runs the full approval lifecycle for the active
// turn matching the request. On error, active is nil when no matching turn exists.
func (cc *CodexClient) resolveApprovalForActiveTurn(
	ctx context.Context, req codexrpc.Request,
	toolName string, inputMap map[string]any,
	presentation sdk.ApprovalPromptPresentation,
) (sdk.ToolApprovalResponse, *codexActiveTurn, error) {
	var params codexApprovalRequestParams
	_ = json.Unmarshal(req.Params, &params)

	cc.activeMu.Lock()
	active := cc.activeTurns[codexTurnKey(params.ThreadID, params.TurnID)]
	cc.activeMu.Unlock()
	if active == nil || params.ThreadID != active.threadID || params.TurnID != active.turnID {
		return sdk.ToolApprovalResponse{}, nil, errors.New("no active turn")
	}

	toolCallID := strings.TrimSpace(params.ItemID)
	if toolCallID == "" {
		toolCallID = toolName
	}
	approvalID := codexApprovalID(req, params.ApprovalID)

	turn := (*sdk.Turn)(nil)
	if active.streamState != nil {
		turn = active.streamState.turn
	}
	if turn != nil {
		turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, inputMap, sdk.ToolInputOptions{
			ToolName:         toolName,
			ProviderExecuted: true,
		})
	}
	handle := cc.requestSDKApproval(ctx, active.portal, active.streamState, turn, sdk.ApprovalRequest{
		ApprovalID:   approvalID,
		ToolCallID:   toolCallID,
		ToolName:     toolName,
		TTL:          10 * time.Minute,
		Presentation: &presentation,
	})

	if active.portalState != nil {
		if lvl, _ := stringutil.NormalizeElevatedLevel(active.portalState.ElevatedLevel); lvl == "full" {
			_ = cc.approvalFlow.Resolve(handle.ID(), sdk.ApprovalDecisionPayload{
				ApprovalID: handle.ID(),
				Approved:   true,
				Reason:     sdk.ApprovalReasonAutoApproved,
			})
		}
	}

	decision, err := handle.Wait(ctx)
	return decision, active, err
}

func (cc *CodexClient) handleApprovalRequest(
	ctx context.Context, req codexrpc.Request,
	defaultToolName string,
	extractInput func(json.RawMessage) (map[string]any, sdk.ApprovalPromptPresentation, codexApprovalBehavior),
) (any, *codexrpc.RPCError) {
	inputMap, presentation, behavior := extractInput(req.Params)
	decision, active, err := cc.resolveApprovalForActiveTurn(ctx, req, defaultToolName, inputMap, presentation)
	if err != nil {
		if active == nil {
			return map[string]any{"decision": "decline"}, nil
		}
		return map[string]any{"decision": "cancel"}, nil
	}
	return map[string]any{"decision": codexApprovalResponseValue(decision.Approved, decision.Always, decision.Reason, behavior.AllowSession)}, nil
}

func (cc *CodexClient) handleCommandApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "commandExecution", func(raw json.RawMessage) (map[string]any, sdk.ApprovalPromptPresentation, codexApprovalBehavior) {
		var p struct {
			Command               *string        `json:"command"`
			Cwd                   *string        `json:"cwd"`
			Reason                *string        `json:"reason"`
			CommandActions        []any          `json:"commandActions"`
			NetworkApproval       map[string]any `json:"networkApprovalContext"`
			AdditionalPermissions map[string]any `json:"additionalPermissions"`
			SkillMetadata         map[string]any `json:"skillMetadata"`
			AvailableDecisions    []any          `json:"availableDecisions"`
		}
		_ = json.Unmarshal(raw, &p)
		input := map[string]any{}
		details := make([]sdk.ApprovalDetail, 0, 8)
		input, details = sdk.AddOptionalDetail(input, details, "command", "Command", p.Command)
		input, details = sdk.AddOptionalDetail(input, details, "cwd", "Working directory", p.Cwd)
		input, details = sdk.AddOptionalDetail(input, details, "reason", "Reason", p.Reason)
		if len(p.CommandActions) > 0 {
			input["commandActions"] = p.CommandActions
			details = append(details, sdk.ApprovalDetail{
				Label: "Command actions",
				Value: sdk.ValueSummary(p.CommandActions),
			})
		}
		if len(p.NetworkApproval) > 0 {
			input["networkApprovalContext"] = p.NetworkApproval
			details = sdk.AppendDetailsFromMap(details, "Network", p.NetworkApproval, 4)
		}
		if len(p.AdditionalPermissions) > 0 {
			input["additionalPermissions"] = p.AdditionalPermissions
			details = codexAppendPermissionDetails(details, p.AdditionalPermissions)
		}
		if len(p.SkillMetadata) > 0 {
			input["skillMetadata"] = p.SkillMetadata
			details = sdk.AppendDetailsFromMap(details, "Skill", p.SkillMetadata, 2)
		}
		details = codexSessionApprovalDetails(details)
		return input, sdk.ApprovalPromptPresentation{
			Title:       "Codex command execution",
			Details:     details,
			AllowAlways: true,
		}, codexApprovalBehavior{AllowSession: true}
	})
}

func (cc *CodexClient) handleFileChangeApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	return cc.handleApprovalRequest(ctx, req, "fileChange", func(raw json.RawMessage) (map[string]any, sdk.ApprovalPromptPresentation, codexApprovalBehavior) {
		var p struct {
			Reason    *string `json:"reason"`
			GrantRoot *string `json:"grantRoot"`
		}
		_ = json.Unmarshal(raw, &p)
		input := map[string]any{}
		details := make([]sdk.ApprovalDetail, 0, 3)
		input, details = sdk.AddOptionalDetail(input, details, "grantRoot", "Grant root", p.GrantRoot)
		input, details = sdk.AddOptionalDetail(input, details, "reason", "Reason", p.Reason)
		details = codexSessionApprovalDetails(details)
		return input, sdk.ApprovalPromptPresentation{
			Title:       "Codex file change",
			Details:     details,
			AllowAlways: true,
		}, codexApprovalBehavior{AllowSession: true}
	})
}

func (cc *CodexClient) handlePermissionsApprovalRequest(ctx context.Context, req codexrpc.Request) (any, *codexrpc.RPCError) {
	var params struct {
		Reason      *string        `json:"reason"`
		Permissions map[string]any `json:"permissions"`
	}
	_ = json.Unmarshal(req.Params, &params)

	input := map[string]any{}
	details := make([]sdk.ApprovalDetail, 0, 6)
	input, details = sdk.AddOptionalDetail(input, details, "reason", "Reason", params.Reason)
	if len(params.Permissions) > 0 {
		input["permissions"] = params.Permissions
		details = codexAppendPermissionDetails(details, params.Permissions)
	}
	details = codexSessionApprovalDetails(details)

	decision, _, err := cc.resolveApprovalForActiveTurn(ctx, req, "permissions", input, sdk.ApprovalPromptPresentation{
		Title:       "Codex permissions request",
		Details:     details,
		AllowAlways: true,
	})
	if err != nil || !decision.Approved {
		return map[string]any{"permissions": map[string]any{}, "scope": "turn"}, nil
	}
	scope := "turn"
	if decision.Always {
		scope = "session"
	}
	return map[string]any{
		"permissions": params.Permissions,
		"scope":       scope,
	}, nil
}
