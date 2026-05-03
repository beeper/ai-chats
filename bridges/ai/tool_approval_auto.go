package ai

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

const ToolApprovalKindMCP = "mcp"

type ToolApprovalParams struct {
	ApprovalID   string
	RoomID       id.RoomID
	TurnID       string
	ToolCallID   string
	ToolName     string
	ToolKind     string
	RuleToolName string
	ServerLabel  string
	Presentation *sdk.ApprovalPromptPresentation
	TTL          time.Duration
}

type autoApprovalHandle struct {
	approvalID string
	toolCallID string
}

func stableMCPApprovalID(toolCallID string, desc responseToolDescriptor) string {
	input := stringifyJSONValue(desc.input)
	return "mcp_approval_" + stringutil.ShortHash(toolCallID+"\n"+desc.toolName+"\n"+input, 8)
}

func buildMCPApprovalPresentation(serverLabel, toolName string, input any) *sdk.ApprovalPromptPresentation {
	return nil
}

func (oc *AIClient) toolApprovalsTTLSeconds() int {
	return 0
}

func (oc *AIClient) toolApprovalsRequireForMCP() bool {
	return false
}

func (oc *AIClient) toolApprovalsRuntimeEnabled() bool {
	return false
}

func (oc *AIClient) isMcpAlwaysAllowed(context.Context, string, string) bool {
	return true
}

func (h autoApprovalHandle) ID() string {
	return h.approvalID
}

func (h autoApprovalHandle) ToolCallID() string {
	return h.toolCallID
}

func (h autoApprovalHandle) Wait(context.Context) (sdk.ToolApprovalResponse, error) {
	return sdk.ToolApprovalResponse{Approved: true, Reason: sdk.ApprovalReasonAutoApproved}, nil
}

func (oc *AIClient) startStreamingMCPApproval(
	ctx context.Context,
	portal *bridgev2.Portal,
	state *streamingState,
	params ToolApprovalParams,
	needsPrompt bool,
) (sdk.ApprovalHandle, error) {
	_ = ctx
	_ = portal
	_ = state
	_ = needsPrompt
	return autoApprovalHandle{approvalID: params.ApprovalID, toolCallID: params.ToolCallID}, nil
}

func (oc *AIClient) waitForToolApprovalResponse(ctx context.Context, handle sdk.ApprovalHandle) sdk.ToolApprovalResponse {
	if handle == nil {
		return sdk.ToolApprovalResponse{Approved: true, Reason: sdk.ApprovalReasonAutoApproved}
	}
	resp, err := handle.Wait(ctx)
	if err != nil {
		return sdk.ToolApprovalResponse{Approved: false, Reason: sdk.ApprovalReasonCancelled}
	}
	if resp.Reason == "" {
		resp.Reason = sdk.ApprovalReasonAutoApproved
	}
	return resp
}
