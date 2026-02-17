package codex

import (
	"go.mau.fi/util/dbutil"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

const codexBridgeVersionTable = "codex_bridge_version"

func makeCodexBridgeChildDB(base *dbutil.Database, log dbutil.DatabaseLogger) *dbutil.Database {
	return bridgeadapter.MakeMemoryChildDB(base, codexBridgeVersionTable, log)
}

func (cc *CodexConnector) bridgeDB() *dbutil.Database {
	if cc == nil {
		return nil
	}
	if cc.db != nil {
		return cc.db
	}
	if cc.br != nil && cc.br.DB != nil {
		cc.db = makeCodexBridgeChildDB(
			cc.br.DB.Database,
			dbutil.ZeroLogger(cc.br.Log.With().Str("db_section", "codex_bridge").Logger()),
		)
		return cc.db
	}
	return nil
}
