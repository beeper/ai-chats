package ai

import (
	"context"
	"reflect"
	"testing"

	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

type fakeToolIntegration struct {
	name string
	defs []integrationruntime.ToolDefinition
}

func (f fakeToolIntegration) Name() string { return f.name }

func (f fakeToolIntegration) ToolDefinitions(_ context.Context, _ integrationruntime.ToolScope) []integrationruntime.ToolDefinition {
	return f.defs
}

func (f fakeToolIntegration) ExecuteTool(_ context.Context, _ integrationruntime.ToolCall) (bool, string, error) {
	return false, "", nil
}

func (f fakeToolIntegration) ToolAvailability(_ context.Context, _ integrationruntime.ToolScope, _ string) (bool, bool, integrationruntime.SettingSource, string) {
	return false, false, integrationruntime.SourceGlobalDefault, ""
}

func TestToolIntegrationRegistryDefinitionsDeterministic(t *testing.T) {
	reg := &toolIntegrationRegistry{}
	reg.register(fakeToolIntegration{name: "one", defs: []integrationruntime.ToolDefinition{{Name: "a"}, {Name: "b"}}})
	reg.register(fakeToolIntegration{name: "two", defs: []integrationruntime.ToolDefinition{{Name: "b"}, {Name: "c"}}})

	defs := reg.definitions(context.Background(), integrationruntime.ToolScope{})
	got := make([]string, 0, len(defs))
	for _, def := range defs {
		got = append(got, def.Name)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected tool merge order: got=%v want=%v", got, want)
	}
}

type fakeLifecycleIntegration struct {
	startCount int
	stopCount  int
	stopOrder  *[]string
	name       string
}

func (f *fakeLifecycleIntegration) Start(_ context.Context) error {
	f.startCount++
	return nil
}

func (f *fakeLifecycleIntegration) Stop() {
	f.stopCount++
	if f.stopOrder != nil {
		*f.stopOrder = append(*f.stopOrder, f.name)
	}
}

func TestLifecycleIntegrationsStartStopOnce(t *testing.T) {
	stopOrder := make([]string, 0, 2)
	first := &fakeLifecycleIntegration{name: "first", stopOrder: &stopOrder}
	second := &fakeLifecycleIntegration{name: "second", stopOrder: &stopOrder}

	client := &AIClient{}
	client.registerIntegrationModule("first", first)
	client.registerIntegrationModule("second", second)

	client.startLifecycleIntegrations(context.Background())
	if first.startCount != 1 || second.startCount != 1 {
		t.Fatalf("expected one start call each, got first=%d second=%d", first.startCount, second.startCount)
	}

	client.stopLifecycleIntegrations()
	if first.stopCount != 1 || second.stopCount != 1 {
		t.Fatalf("expected one stop call each, got first=%d second=%d", first.stopCount, second.stopCount)
	}
	if !reflect.DeepEqual(stopOrder, []string{"second", "first"}) {
		t.Fatalf("unexpected stop order: got=%v want=%v", stopOrder, []string{"second", "first"})
	}
}
