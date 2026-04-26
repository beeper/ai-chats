package toolpolicy

import "testing"

func TestNormalizeToolNameRemovesAnalyzeImageAlias(t *testing.T) {
	if got := NormalizeToolName("analyze_image"); got != "analyze_image" {
		t.Fatalf("expected analyze_image to stay unchanged, got %q", got)
	}
}

func TestNormalizeToolNameLeavesApplyPatchUnchanged(t *testing.T) {
	if got := NormalizeToolName("apply-patch"); got != "apply-patch" {
		t.Fatalf("expected apply-patch to stay unchanged, got %q", got)
	}
}

func TestNormalizeToolNameLeavesBashUnchanged(t *testing.T) {
	if got := NormalizeToolName("bash"); got != "bash" {
		t.Fatalf("expected bash to stay unchanged, got %q", got)
	}
}

func TestExpandToolGroups_Runtime(t *testing.T) {
	got := ExpandToolGroups([]string{"group:runtime"})
	if len(got) != 2 || got[0] != "exec" || got[1] != "process" {
		t.Fatalf("unexpected group:runtime expansion: %#v", got)
	}
}

func TestExpandToolGroups_AgentRemote(t *testing.T) {
	got := ExpandToolGroups([]string{"group:agentremote"})
	mustContain := []string{
		"message",
		"cron",
		"sessions_list",
		"sessions_send",
		"web_search",
		"web_fetch",
		"image",
		"browser",
		"canvas",
		"nodes",
		"gateway",
		"beeper_docs",
		"beeper_send_feedback",
		"gravatar_fetch",
		"gravatar_set",
		"tts",
		"image_generate",
		"calculator",
	}
	for _, name := range mustContain {
		if !containsString(got, name) {
			t.Fatalf("expected group:agentremote to include %q, got %#v", name, got)
		}
	}
}

func containsString(list []string, value string) bool {
	for _, entry := range list {
		if entry == value {
			return true
		}
	}
	return false
}
