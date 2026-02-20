package connector

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/openai/openai-go/v3"

	integrationruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
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

type fakePromptIntegration struct {
	name string
	tag  string
}

func (f fakePromptIntegration) Name() string { return f.name }

func (f fakePromptIntegration) AdditionalSystemMessages(_ context.Context, _ integrationruntime.PromptScope) []openai.ChatCompletionMessageParamUnion {
	return []openai.ChatCompletionMessageParamUnion{openai.SystemMessage("sys:" + f.tag)}
}

func (f fakePromptIntegration) AugmentPrompt(
	_ context.Context,
	_ integrationruntime.PromptScope,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(prompt)+1)
	out = append(out, prompt...)
	out = append(out, openai.UserMessage("aug:"+f.tag))
	return out
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

func TestPromptIntegrationRegistryOrder(t *testing.T) {
	reg := &promptIntegrationRegistry{}
	reg.register(fakePromptIntegration{name: "one", tag: "1"})
	reg.register(fakePromptIntegration{name: "two", tag: "2"})

	sys := reg.additionalMessages(context.Background(), integrationruntime.PromptScope{})
	if len(sys) != 2 {
		t.Fatalf("expected 2 system messages, got %d", len(sys))
	}

	base := []openai.ChatCompletionMessageParamUnion{openai.UserMessage("base")}
	out := reg.augmentPrompt(context.Background(), integrationruntime.PromptScope{}, base)
	if len(out) != 3 {
		t.Fatalf("expected augmented prompt len=3, got %d", len(out))
	}
}

func TestPromptIntegrationRegistryAugmentPromptIdempotent(t *testing.T) {
	reg := &promptIntegrationRegistry{}
	reg.register(fakePromptIntegration{name: "one", tag: "1"})
	reg.register(fakePromptIntegration{name: "two", tag: "2"})

	base := []openai.ChatCompletionMessageParamUnion{openai.UserMessage("base")}
	baseCopy := slices.Clone(base)

	outA := reg.augmentPrompt(context.Background(), integrationruntime.PromptScope{}, base)
	outB := reg.augmentPrompt(context.Background(), integrationruntime.PromptScope{}, base)
	if !reflect.DeepEqual(outA, outB) {
		t.Fatalf("augmentPrompt should be deterministic/idempotent; got outA=%v outB=%v", outA, outB)
	}
	if !reflect.DeepEqual(base, baseCopy) {
		t.Fatalf("augmentPrompt mutated input prompt; got=%v want=%v", base, baseCopy)
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
