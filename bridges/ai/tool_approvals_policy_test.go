package ai

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

func TestBuiltinToolApprovalRequirement_MessageDesktopReadOnlyDoesNotRequireApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("message", map[string]any{"action": "desktop-search-chats"})
	if required {
		t.Fatalf("expected required=false for desktop-search-chats")
	}
	if action != "desktop-search-chats" {
		t.Fatalf("expected action=desktop-search-chats, got %q", action)
	}
}

func TestBuiltinToolApprovalRequirement_MessageDesktopSendRequiresApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("message", map[string]any{"action": "send"})
	if !required {
		t.Fatalf("expected required=true for send")
	}
	if action != "send" {
		t.Fatalf("expected action=send, got %q", action)
	}
}

func TestBuiltinToolApprovalRequirement_MessageDesktopCreateChatRequiresApproval(t *testing.T) {
	oc := &AIClient{connector: &OpenAIConnector{}}

	required, action := oc.builtinToolApprovalRequirement("message", map[string]any{"action": "desktop-create-chat"})
	if !required {
		t.Fatalf("expected required=true for desktop-create-chat")
	}
	if action != "desktop-create-chat" {
		t.Fatalf("expected action=desktop-create-chat, got %q", action)
	}
}
