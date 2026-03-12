package agentremote

// ApprovalManager is the public approval facade for bridge builders. It wraps
// the generic ApprovalFlow with a clearer runtime-facing name.
type ApprovalManager[D any] struct {
	*ApprovalFlow[D]
}

func NewApprovalManager[D any](cfg ApprovalFlowConfig[D]) *ApprovalManager[D] {
	return &ApprovalManager[D]{ApprovalFlow: NewApprovalFlow(cfg)}
}
