package ai

import (
	"context"
	"testing"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

type stubToolIntegration struct {
	execute func(context.Context, integrationruntime.ToolCall) (bool, string, error)
}

func (s *stubToolIntegration) Name() string {
	return "stub"
}

func (s *stubToolIntegration) ToolDefinitions(context.Context, integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	return nil
}

func (s *stubToolIntegration) ExecuteTool(ctx context.Context, call integrationruntime.ToolCall) (bool, string, error) {
	if s.execute == nil {
		return false, "", nil
	}
	return s.execute(ctx, call)
}

func (s *stubToolIntegration) ToolAvailability(context.Context, integrationruntime.ToolScope, string) (bool, bool, integrationruntime.SettingSource, string) {
	return false, false, integrationruntime.SourceGlobalDefault, ""
}

func TestParseToolArgsPreservesNonObjectJSON(t *testing.T) {
	argsJSON, args, err := parseToolArgs(`["a","b"]`)
	if err != nil {
		t.Fatalf("parseToolArgs returned error: %v", err)
	}
	if argsJSON != `["a","b"]` {
		t.Fatalf("expected original JSON to be preserved, got %q", argsJSON)
	}
	if args != nil {
		t.Fatalf("expected non-object JSON to produce nil args map, got %#v", args)
	}
}

func TestExecuteBuiltinToolPassesRawNonObjectJSONToIntegrations(t *testing.T) {
	invoked := 0
	oc := &AIClient{
		toolRegistry: &toolIntegrationRegistry{
			items: []integrationruntime.ToolIntegration{
				&stubToolIntegration{
					execute: func(_ context.Context, call integrationruntime.ToolCall) (bool, string, error) {
						invoked++
						if call.Name != "custom_tool" {
							t.Fatalf("expected tool name custom_tool, got %q", call.Name)
						}
						if call.RawArgsJSON != `["a","b"]` {
							t.Fatalf("expected raw args to be preserved, got %q", call.RawArgsJSON)
						}
						if call.Args != nil {
							t.Fatalf("expected nil args for non-object payload, got %#v", call.Args)
						}
						return true, "ok", nil
					},
				},
			},
		},
	}

	result, err := oc.executeBuiltinTool(context.Background(), nil, "custom_tool", `["a","b"]`)
	if err != nil {
		t.Fatalf("executeBuiltinTool returned error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected integration result ok, got %q", result)
	}
	if invoked != 1 {
		t.Fatalf("expected integration to be invoked once, got %d", invoked)
	}
}

func TestExecuteBuiltinToolAcceptsNonObjectJSONWithoutParseFailure(t *testing.T) {
	oc := &AIClient{}

	_, err := oc.executeBuiltinTool(context.Background(), nil, "unknown_tool", `["a","b"]`)
	if err == nil {
		t.Fatal("expected unknown tool error")
	}
	if err.Error() != "unknown tool: unknown_tool" {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestExecuteBuiltinToolRejectsOwnerOnlyToolBeforeIntegratedHandlers(t *testing.T) {
	invoked := 0
	oc := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Commands: &CommandsConfig{OwnerAllowFrom: []string{"@owner:example.com"}},
			},
		},
		toolRegistry: &toolIntegrationRegistry{
			items: []integrationruntime.ToolIntegration{
				&stubToolIntegration{
					execute: func(_ context.Context, call integrationruntime.ToolCall) (bool, string, error) {
						invoked++
						return true, "should-not-run", nil
					},
				},
			},
		},
	}

	ctx := WithBridgeToolContext(context.Background(), &BridgeToolContext{SenderID: "@other:example.com"})
	_, err := oc.executeBuiltinTool(ctx, nil, "whatsapp_login", `{}`)
	if err == nil || err.Error() != "tool restricted to owner senders" {
		t.Fatalf("expected owner-only restriction error, got %v", err)
	}
	if invoked != 0 {
		t.Fatalf("expected integration handler not to run, got %d invocations", invoked)
	}
}
