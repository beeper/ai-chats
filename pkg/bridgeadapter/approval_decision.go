package bridgeadapter

type ApprovalDecisionPayload struct {
	ApprovalID string
	Approved   bool
	Always     bool
	Reason     string
}
