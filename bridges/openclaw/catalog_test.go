package openclaw

import "testing"

func TestPreviewSnippetForSession(t *testing.T) {
	resp := gatewaySessionsPreviewResponse{
		Previews: []gatewaySessionPreviewEntry{
			{
				Key:    "agent:main:matrix-dm",
				Status: "ok",
				Items: []gatewaySessionPreviewItem{
					{Role: "user", Text: "hello"},
					{Role: "assistant", Text: "world"},
				},
			},
		},
	}
	if got := previewSnippetForSession(resp, "agent:main:matrix-dm"); got != "hello world" {
		t.Fatalf("unexpected preview snippet: %q", got)
	}
}

func TestSummarizeToolsCatalog(t *testing.T) {
	count, profile := summarizeToolsCatalog(gatewayToolsCatalogResponse{
		Profiles: []gatewayToolCatalogProfile{{ID: "default", Label: "Default"}},
		Groups: []gatewayToolCatalogGroup{
			{Tools: []gatewayToolCatalogEntry{{ID: "tool-1"}, {ID: "tool-2"}}},
			{Tools: []gatewayToolCatalogEntry{{ID: "tool-3"}}},
		},
	})
	if count != 3 || profile != "Default" {
		t.Fatalf("unexpected tool summary: count=%d profile=%q", count, profile)
	}
}
