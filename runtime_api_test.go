package agentremote

import "testing"

func TestNewApprovalFlowInit(t *testing.T) {
	flow := NewApprovalFlow[map[string]any](ApprovalFlowConfig[map[string]any]{})
	if flow == nil {
		t.Fatal("expected approval flow")
	}
}
