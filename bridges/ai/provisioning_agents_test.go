package ai

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListAgentsForResponseDisabledReturnsEmpty(t *testing.T) {
	disabled := false
	client := newCatalogTestClient()
	client.connector.Config.Agents = &AgentsConfig{Enabled: &disabled}

	items, err := listAgentsForResponse(context.Background(), client, NewAgentStoreAdapter(client))
	if err != nil {
		t.Fatalf("listAgentsForResponse returned error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no agents when disabled, got %#v", items)
	}
}

func TestWriteAgentErrorDisabledReturnsForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAgentError(rec, errAgentsDisabled)

	if rec.Code != 403 {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "agents are disabled") {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
}

func TestWriteAgentErrorDisabledMatchesSentinel(t *testing.T) {
	if !errors.Is(errAgentsDisabled, errAgentsDisabled) {
		t.Fatal("expected sentinel error to match itself")
	}
}
