package ai

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteToolInContextRejectsDisabledTool(t *testing.T) {
	host := &runtimeIntegrationHost{
		client: &AIClient{
			connector: &OpenAIConnector{Config: Config{}},
		},
	}

	_, err := host.ExecuteToolInContext(context.Background(), nil, &PortalMetadata{
		DisabledTools: []string{ToolNameMessage},
	}, ToolNameMessage, `{}`)
	if err == nil {
		t.Fatal("expected disabled tool error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled tool error, got %v", err)
	}
}
