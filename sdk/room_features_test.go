package sdk

import "testing"

func TestComputeRoomFeaturesForAgentsUsesStrictMinimum(t *testing.T) {
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
				SupportsTextInput:   true,
				SupportsImageInput:  false,
				SupportsFilesOutput: false,
				MaxTextLength:       5000,
			},
		},
	})
	if features.MaxTextLength != 5000 {
		t.Fatalf("expected min text length 5000, got %d", features.MaxTextLength)
	}
	if features.SupportsTyping {
		t.Fatalf("expected typing to require all agents to support streaming")
	}
	if features.SupportsImages {
		t.Fatalf("expected image capability to require common support")
	}
	if !features.SupportsReply {
		t.Fatalf("expected reply support when all agents support text input")
	}
}
