package connector

import "testing"

func TestResolveGroupHistoryLimitDefaults(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{},
		},
	}
	if got := client.resolveGroupHistoryLimit(); got != defaultGroupHistoryLimit {
		t.Fatalf("expected default group history limit %d, got %d", defaultGroupHistoryLimit, got)
	}
}

func TestResolveGroupHistoryLimitConfigOverride(t *testing.T) {
	client := &AIClient{
		connector: &OpenAIConnector{
			Config: Config{
				Messages: &MessagesConfig{
					GroupChat: &GroupChatConfig{HistoryLimit: 7},
				},
			},
		},
	}
	if got := client.resolveGroupHistoryLimit(); got != 7 {
		t.Fatalf("expected configured group history limit 7, got %d", got)
	}
}
