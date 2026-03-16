package ai

import (
	"context"
	"strings"
	"testing"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

func TestExecuteBuiltinToolRejectsDisabledTool(t *testing.T) {
	host := &runtimeIntegrationHost{
		client: &AIClient{
			connector: &OpenAIConnector{Config: Config{}},
		},
	}

	_, err := host.ExecuteBuiltinTool(context.Background(), integrationruntime.ToolScope{
		Meta: &PortalMetadata{
			DisabledTools: []string{ToolNameMessage},
		},
	}, ToolNameMessage, `{}`)
	if err == nil {
		t.Fatal("expected disabled tool error")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled tool error, got %v", err)
	}
}
