package connector

import "testing"

func TestBuiltinToolApprovalRequirement_Write_DoesNotRequireApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("write", map[string]any{"path": "notes/a.txt"})
	if required {
		t.Fatalf("expected required=false for write")
	}
	if action != "" {
		t.Fatalf("expected empty action, got %q", action)
	}
}

func TestBuiltinToolApprovalRequirement_Edit_DoesNotRequireApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("edit", map[string]any{"path": "notes/a.txt"})
	if required {
		t.Fatalf("expected required=false for edit")
	}
	if action != "" {
		t.Fatalf("expected empty action, got %q", action)
	}
}

func TestBuiltinToolApprovalRequirement_ApplyPatch_DoesNotRequireApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("apply_patch", map[string]any{
		"input": "*** Begin Patch\n*** End Patch",
	})
	if required {
		t.Fatalf("expected required=false for apply_patch")
	}
	if action != "" {
		t.Fatalf("expected empty action, got %q", action)
	}
}
