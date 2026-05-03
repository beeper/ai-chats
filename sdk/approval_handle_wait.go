package sdk

import "context"

type WaitToolApprovalHandleParams struct {
	Turn             *Turn
	ApprovalID       string
	ToolCallID       string
	DenyToolOnReject bool
}

func WaitToolApprovalHandle(
	ctx context.Context,
	params WaitToolApprovalHandleParams,
	wait func(context.Context) (ToolApprovalResponse, error),
) (ToolApprovalResponse, error) {
	if wait == nil {
		return ToolApprovalResponse{}, nil
	}
	resp, err := wait(ctx)
	if err != nil {
		return resp, err
	}
	if params.Turn == nil {
		return resp, nil
	}
	params.Turn.Approvals().Respond(params.Turn.Context(), params.ApprovalID, params.ToolCallID, resp.Approved, resp.Reason)
	if params.DenyToolOnReject && !resp.Approved {
		params.Turn.Writer().Tools().Denied(params.Turn.Context(), params.ToolCallID)
	}
	return resp, nil
}
