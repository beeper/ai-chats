package openclaw

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beeper/agentremote/pkg/shared/openclawconv"
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

	agents, err := client.ListAgents(ctx)
	if err != nil {
		t.Fatalf("agents.list: %v", err)
	}
	if agents == nil {
		t.Fatal("expected non-nil agents.list response")
	}

	sessions, err := client.ListSessions(ctx, 20)
	if err != nil {
		t.Fatalf("sessions.list: %v", err)
	}
	sessionKey := strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_SESSION_KEY"))
	if sessionKey == "" && len(sessions) > 0 {
		sessionKey = sessions[0].Key
	}
	if sessionKey != "" {
		history, err := client.SessionHistory(ctx, sessionKey, 10, "")
		if err != nil {
			t.Fatalf("session history: %v", err)
		}
		if history == nil {
			t.Fatal("expected non-nil history response")
		}

		agentID := openclawconv.AgentIDFromSessionKey(sessionKey)
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

	dmAgentID := strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_DM_AGENT_ID"))
	if dmAgentID == "" && agents != nil {
		dmAgentID = strings.TrimSpace(agents.DefaultID)
	}
	if dmAgentID != "" {
		dmSessionKey := openClawDMAgentSessionKey(dmAgentID)
		if openclawconv.AgentIDFromSessionKey(dmSessionKey) != dmAgentID {
			t.Fatalf("expected synthetic dm session key for %q, got %q", dmAgentID, dmSessionKey)
		}
		if message := strings.TrimSpace(os.Getenv("OPENCLAW_SMOKE_SEND_MESSAGE")); message != "" {
			resp, err := client.SendMessage(ctx, dmSessionKey, message, nil, "", "", "smoke-"+time.Now().UTC().Format("20060102150405"))
			if err != nil {
				t.Fatalf("chat.send synthetic dm: %v", err)
			}
			if resp == nil || strings.TrimSpace(resp.RunID) == "" {
				t.Fatal("expected non-empty run id from synthetic dm send")
			}
		}
	}
}
