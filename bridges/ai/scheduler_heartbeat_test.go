package ai

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/aidb"
)

func testAgentPortal(portalID, roomID, agentID string, meta *PortalMetadata) *bridgev2.Portal {
	return &bridgev2.Portal{
		Portal: &database.Portal{
			PortalKey:   networkid.PortalKey{ID: networkid.PortalID(portalID)},
			MXID:        id.RoomID(roomID),
			OtherUserID: agentUserID(agentID),
			Metadata:    meta,
		},
	}
}

func TestAgentHasUserChat(t *testing.T) {
	portals := []*bridgev2.Portal{
		testAgentPortal("chat-1", "!chat1:example.com", "beeper", &PortalMetadata{Title: "Chat"}),
		testAgentPortal("heartbeat", "!hb:example.com", "beeper", &PortalMetadata{
			ModuleMeta: map[string]any{"heartbeat": map[string]any{"is_internal_room": true}},
		}),
		testAgentPortal("subagent", "!sub:example.com", "beeper", &PortalMetadata{
			SubagentParentRoomID: "!parent:example.com",
		}),
	}

	if !agentHasUserChat(portals, "beeper") {
		t.Fatal("expected beeper to have a user chat")
	}
	if agentHasUserChat(portals, "worker") {
		t.Fatal("expected worker to have no user chat")
	}
	// Internal and subagent rooms should not count.
	internalOnly := []*bridgev2.Portal{portals[1], portals[2]}
	if agentHasUserChat(internalOnly, "beeper") {
		t.Fatal("expected internal-only portals not to count as user chats")
	}
}

func TestSchedulableHeartbeatAgents_DoesNotRequirePortalListing(t *testing.T) {
	enabled := true
	runtime := &schedulerRuntime{
		client: &AIClient{
			UserLogin: &bridgev2.UserLogin{
				UserLogin: &database.UserLogin{
					Metadata: &UserLoginMetadata{Agents: &enabled},
				},
			},
			connector: &OpenAIConnector{Config: Config{}},
			log:       zerolog.Nop(),
		},
	}

	agents, err := runtime.schedulableHeartbeatAgents(context.Background())
	if err != nil {
		t.Fatalf("schedulableHeartbeatAgents returned error without bridge access: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected one schedulable heartbeat agent, got %d", len(agents))
	}
	if agents[0].agentID != "beeper" {
		t.Fatalf("expected default heartbeat agent to be beeper, got %q", agents[0].agentID)
	}
}

func TestRequestHeartbeatNow_SkipsAgentsWithoutDeliveryTarget(t *testing.T) {
	runtime, childDB := newHeartbeatSchedulerTestRuntime(t, Config{})

	runtime.RequestHeartbeatNow(context.Background(), "test")

	var count int
	err := childDB.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM aichats_managed_heartbeats
		WHERE bridge_id=$1 AND login_id=$2
	`, "bridge", "login").Scan(&count)
	if err != nil {
		t.Fatalf("count managed heartbeats: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no managed heartbeat rows without a delivery target, got %d", count)
	}
}

func newHeartbeatSchedulerTestRuntime(t *testing.T, cfg Config) (*schedulerRuntime, *dbutil.Database) {
	t.Helper()

	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	baseDB, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap sqlite db: %v", err)
	}
	bridgeDB := database.New(networkid.BridgeID("bridge"), database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
	}, baseDB)
	if err := bridgeDB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}

	childDB := aidb.NewChild(bridgeDB.Database, dbutil.NoopLogger)
	if err := aidb.Upgrade(context.Background(), childDB, "agentremote", "database not initialized"); err != nil {
		t.Fatalf("upgrade agentremote db: %v", err)
	}

	enabled := true
	login := &database.UserLogin{
		ID:       networkid.UserLoginID("login"),
		Metadata: &UserLoginMetadata{Agents: &enabled},
	}
	userLogin := &bridgev2.UserLogin{
		UserLogin: login,
		Bridge:    &bridgev2.Bridge{DB: bridgeDB, Config: &bridgeconfig.BridgeConfig{}},
		Log:       zerolog.Nop(),
	}
	client := &AIClient{
		UserLogin: userLogin,
		connector: &OpenAIConnector{Config: cfg},
		log:       zerolog.Nop(),
	}

	return &schedulerRuntime{client: client}, childDB
}
