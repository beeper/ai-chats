package ai

import (
	"context"
	"testing"
)

func TestPurgeLoginData_RemovesRunKeyTables(t *testing.T) {
	ctx := context.Background()
	client := newDBBackedTestAIClient(t, ProviderOpenAI)
	client.UserLogin.Client = client

	db := client.bridgeDB()
	if db == nil {
		t.Fatalf("expected bridge db")
	}
	bridgeID := string(client.UserLogin.Bridge.DB.BridgeID)
	loginID := string(client.UserLogin.ID)

	if _, err := db.Exec(ctx, `INSERT INTO `+aiCronJobRunKeysTable+` (bridge_id, login_id, job_id, run_index, run_key) VALUES ($1, $2, $3, $4, $5)`,
		bridgeID, loginID, "job-1", 1, "run-1",
	); err != nil {
		t.Fatalf("insert cron run key: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO `+aiHeartbeatRunKeysTable+` (bridge_id, login_id, agent_id, run_index, run_key) VALUES ($1, $2, $3, $4, $5)`,
		bridgeID, loginID, "agent-1", 1, "run-2",
	); err != nil {
		t.Fatalf("insert heartbeat run key: %v", err)
	}

	purgeLoginData(ctx, client.UserLogin)

	for _, table := range []string{aiCronJobRunKeysTable, aiHeartbeatRunKeysTable} {
		var count int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table+` WHERE bridge_id=$1 AND login_id=$2`, bridgeID, loginID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("expected %s rows to be purged, found %d", table, count)
		}
	}
}
