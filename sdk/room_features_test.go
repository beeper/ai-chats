package sdk

import "testing"

func TestComputeRoomFeaturesForAgentsUsesUnionSemantics(t *testing.T) {
	features := computeRoomFeaturesForAgents([]*Agent{
		{
			ID: "a",
			Capabilities: AgentCapabilities{
				SupportsStreaming:   true,
				SupportsReasoning:   true,
				SupportsToolCalling: true,
				SupportsTextInput:   true,
				SupportsImageInput:  true,
				SupportsFilesOutput: true,
				MaxTextLength:       12000,
			},
		},
		{
			ID: "b",
			Capabilities: AgentCapabilities{
				SupportsStreaming:   false,
				SupportsReasoning:   true,
				SupportsToolCalling: false,
				SupportsTextInput:   false,
				SupportsImageInput:  false,
				SupportsFilesOutput: false,
				MaxTextLength:       5000,
			},
		},
	})
	if features.MaxTextLength != 12000 {
		t.Fatalf("expected max text length 12000, got %d", features.MaxTextLength)
	}
	if !features.SupportsTyping {
		t.Fatalf("expected typing to be enabled when any agent supports streaming")
	}
	if !features.SupportsImages {
		t.Fatalf("expected image capability when any agent supports image input")
	}
	if !features.SupportsReply {
		t.Fatalf("expected reply support when any agent supports text input")
	}
}
