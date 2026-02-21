package connector

import (
	"context"
	"testing"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
)

func TestInjectMemoryContextDisabledByDefault(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{},
		},
	}
	in := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
	}
	out := client.injectMemoryContext(context.Background(), &bridgev2.Portal{}, &PortalMetadata{}, in)
	if len(out) != len(in) {
		t.Fatalf("expected unchanged prompt length %d, got %d", len(in), len(out))
	}
}

func TestInjectMemoryContextDisabledWhenConfigOff(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Memory: &MemoryConfig{
					InjectContext: false,
				},
			},
		},
	}
	in := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage("system"),
	}
	out := client.injectMemoryContext(context.Background(), &bridgev2.Portal{}, &PortalMetadata{}, in)
	if len(out) != len(in) {
		t.Fatalf("expected unchanged prompt length %d, got %d", len(in), len(out))
	}
}
