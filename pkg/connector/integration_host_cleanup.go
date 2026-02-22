package connector

import (
	"context"
	"strings"

	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

func loadMemoryChunkIDsByAgentBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) map[string][]string {
	return integrationmemory.LoadChunkIDsByAgentBestEffort(ctx, db, bridgeID, loginID)
}

func purgeAIMemoryTablesBestEffort(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) {
	integrationmemory.PurgeTablesBestEffort(ctx, db, bridgeID, loginID)
}

func purgeVectorRowsBestEffort(ctx context.Context, login *bridgev2.UserLogin, bridgeID, loginID string) {
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return
	}
	db := bridgeDBFromLogin(login)
	if db == nil {
		return
	}
	client, ok := login.Client.(*AIClient)
	if !ok || client == nil {
		return
	}
	cfg, err := resolveMemorySearchConfig(client, "")
	if err != nil || cfg == nil || !cfg.Store.Vector.Enabled {
		return
	}
	extPath := strings.TrimSpace(cfg.Store.Vector.ExtensionPath)
	if extPath == "" {
		return
	}
	integrationmemory.PurgeVectorRowsBestEffort(ctx, db, bridgeID, loginID, extPath)
}
