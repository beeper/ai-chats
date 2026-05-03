package aihelpers

import (
	"context"
	"strings"
)

type WaitApprovalParams[D any] struct {
	BuildNoDecision func(reason string, data D) *ApprovalDecisionPayload
	OnResolved      func(context.Context, ApprovalDecisionPayload, D)
}

func (f *ApprovalFlow[D]) WaitAndFinalizeApproval(
	ctx context.Context,
	approvalID string,
	params WaitApprovalParams[D],
) (ApprovalDecisionPayload, D, bool) {
	var zeroData D
	if f == nil {
		return ApprovalDecisionPayload{}, zeroData, false
	}
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return ApprovalDecisionPayload{}, zeroData, false
	}
	pending := f.Get(approvalID)
	if pending == nil {
		return ApprovalDecisionPayload{}, zeroData, false
	}
	data := pending.Data
	decision, ok := f.Wait(ctx, approvalID)
	if !ok {
		reason := ApprovalWaitReason(ctx)
		if params.BuildNoDecision != nil {
			finalDecision := params.BuildNoDecision(reason, data)
			if finalDecision != nil {
				if strings.TrimSpace(finalDecision.ApprovalID) == "" {
					finalDecision.ApprovalID = approvalID
				}
				f.FinishResolved(approvalID, *finalDecision)
				return *finalDecision, data, false
			}
		}
		return ApprovalDecisionPayload{
			ApprovalID: approvalID,
			Reason:     reason,
		}, data, false
	}
	if params.OnResolved != nil {
		params.OnResolved(ctx, decision, data)
	}
	f.FinishResolved(approvalID, decision)
	return decision, data, true
}
