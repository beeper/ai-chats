package connector

import (
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/cron"
)

func TestReadCronJobID_RequiresCanonicalJobID(t *testing.T) {
	if got := readCronJobID(map[string]any{"jobId": "job-123"}); got != "job-123" {
		t.Fatalf("expected jobId to be preferred, got %q", got)
	}
	if got := readCronJobID(map[string]any{"id": "legacy-456"}); got != "" {
		t.Fatalf("expected legacy id alias to be rejected, got %q", got)
	}
	if got := readCronJobID(map[string]any{"jobId": "  ", "id": "fallback-789"}); got != "" {
		t.Fatalf("expected empty job id when canonical jobId is blank, got %q", got)
	}
}

func TestInjectCronContext_SetsDeliveryTargetToCurrentRoom(t *testing.T) {
	job := cron.CronJobCreate{
		SessionTarget: cron.CronSessionIsolated,
		Payload:       cron.CronPayload{Kind: "agentTurn", Message: "Ping"},
		Delivery:      &cron.CronDelivery{Mode: cron.CronDeliveryAnnounce},
	}
	btc := &BridgeToolContext{
		Portal: &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!room:example.org")}},
		Meta:   &PortalMetadata{AgentID: "beeper"},
	}

	injectCronContext(&job, btc)

	if job.AgentID == nil || *job.AgentID != "beeper" {
		t.Fatalf("expected agent id to be injected, got %#v", job.AgentID)
	}
	if job.Delivery == nil {
		t.Fatalf("expected delivery to stay defined")
	}
	if job.Delivery.Channel != "matrix" {
		t.Fatalf("expected delivery channel matrix, got %q", job.Delivery.Channel)
	}
	if job.Delivery.To != "!room:example.org" {
		t.Fatalf("expected delivery target room to be injected, got %q", job.Delivery.To)
	}
}

func TestInjectCronContext_DoesNotPinDeliveryForCronRoom(t *testing.T) {
	job := cron.CronJobCreate{
		SessionTarget: cron.CronSessionIsolated,
		Payload:       cron.CronPayload{Kind: "agentTurn", Message: "Ping"},
		Delivery:      &cron.CronDelivery{Mode: cron.CronDeliveryAnnounce},
	}
	btc := &BridgeToolContext{
		Portal: &bridgev2.Portal{Portal: &database.Portal{MXID: id.RoomID("!cronroom:example.org")}},
		Meta:   &PortalMetadata{AgentID: "beeper", IsCronRoom: true},
	}

	injectCronContext(&job, btc)

	if job.AgentID == nil || *job.AgentID != "beeper" {
		t.Fatalf("expected agent id to be injected, got %#v", job.AgentID)
	}
	if job.Delivery == nil {
		t.Fatalf("expected delivery to stay defined")
	}
	if strings.TrimSpace(job.Delivery.To) != "" {
		t.Fatalf("expected delivery target to remain unset for cron room source, got %q", job.Delivery.To)
	}
}

func TestValidateCronDeliveryTo(t *testing.T) {
	cases := []struct {
		name    string
		to      string
		wantErr string
	}{
		{name: "empty ok", to: "", wantErr: ""},
		{name: "room ok", to: "!room:example.org", wantErr: ""},
		{name: "user id rejected", to: "@user:example.org", wantErr: "not a user id"},
		{name: "garbage rejected", to: "room:example.org", wantErr: "Matrix room id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCronDeliveryTo(tc.to)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}
