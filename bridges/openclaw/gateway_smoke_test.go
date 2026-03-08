package openclaw

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGatewaySmoke(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_GATEWAY_URL"))
	if url == "" {
		t.Skip("set OPENCLAW_SMOKE_GATEWAY_URL to run gateway smoke test")
	}
	cfg := gatewayConnectConfig{
		URL:      url,
		Token:    strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_GATEWAY_TOKEN")),
		Password: strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_GATEWAY_PASSWORD")),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := newGatewayWSClient(cfg)
	if _, err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	sessions, err := client.ListSessions(ctx, 20)
	if err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one visible session")
	}

	sessionKey := strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_SESSION_KEY"))
	if sessionKey == "" {
		sessionKey = sessions[0].Key
	}
	history, err := client.RecentHistory(ctx, sessionKey, 10)
	if err != nil {
		t.Fatalf("chat.history: %v", err)
	}
	if history == nil {
		t.Fatal("expected non-nil history response")
	}

	agentID := openClawAgentIDFromSessionKey(sessionKey)
	if agentID != "" {
		identity, err := client.GetAgentIdentity(ctx, agentID, sessionKey)
		if err != nil {
			t.Fatalf("agent.identity.get: %v", err)
		}
		if identity == nil || strings.TrimSpace(identity.AgentID) == "" {
			t.Fatal("expected non-empty agent identity")
		}
	}
}
