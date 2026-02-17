package connector

import (
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

const aiBridgeVersionTable = "ai_bridge_version"

func makeBridgeChildDB(base *dbutil.Database, log dbutil.DatabaseLogger) *dbutil.Database {
	return bridgeadapter.MakeMemoryChildDB(base, aiBridgeVersionTable, log)
}

func (oc *OpenAIConnector) bridgeDB() *dbutil.Database {
	if oc == nil {
		return nil
	}
	if oc.db != nil {
		return oc.db
	}
	if oc.br != nil && oc.br.DB != nil {
		oc.db = makeBridgeChildDB(
			oc.br.DB.Database,
			dbutil.ZeroLogger(oc.br.Log.With().Str("db_section", "ai_bridge").Logger()),
		)
		return oc.db
	}
	return nil
}

func (oc *AIClient) bridgeDB() *dbutil.Database {
	if oc == nil {
		return nil
	}
	if oc.connector != nil {
		if db := oc.connector.bridgeDB(); db != nil {
			return db
		}
	}
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.DB != nil {
		return makeBridgeChildDB(oc.UserLogin.Bridge.DB.Database, dbutil.NoopLogger)
	}
	return nil
}

func bridgeDBFromLogin(login *bridgev2.UserLogin) *dbutil.Database {
	if login == nil {
		return nil
	}
	if client, ok := login.Client.(*AIClient); ok && client != nil {
		if db := client.bridgeDB(); db != nil {
			return db
		}
	}
	if login.Bridge != nil && login.Bridge.DB != nil {
		return makeBridgeChildDB(login.Bridge.DB.Database, dbutil.NoopLogger)
	}
	return nil
}
