package connector

import (
	"testing"

	"go.mau.fi/util/ptr"
)

func TestBuildResponsesParamsAcceptsBridgePromptContext(t *testing.T) {
	provider := &OpenAIProvider{}
	params := provider.buildResponsesParams(GenerateParams{
		Model:   "gpt-5.2",
		Context: UserPromptContext(PromptBlock{Type: PromptBlockText, Text: "hello"}),
	})
	if len(params.Input.OfInputItemList) != 1 {
		t.Fatalf("expected one input item, got %d", len(params.Input.OfInputItemList))
	}
}

func TestBuildResponsesParamsPreservesExplicitZeroTemperature(t *testing.T) {
	provider := &OpenAIProvider{}
	params := provider.buildResponsesParams(GenerateParams{
		Model:       "gpt-5.2",
		Temperature: ptr.Ptr(0.0),
	})

	if !params.Temperature.Valid() || params.Temperature.Value != 0 {
		t.Fatalf("expected explicit zero temperature, got %#v", params.Temperature)
	}
}
