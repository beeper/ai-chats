package ai

import (
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/aidb"
)

const (
	aiSessionsTable              = "aichats_sessions"
	aiSystemEventsTable          = "aichats_system_events"
	aiInternalMessagesTable      = "aichats_internal_messages"
	aiLoginStateTable            = "aichats_login_state"
	aiLoginConfigTable           = "aichats_login_config"
	aiCustomAgentsTable          = "aichats_custom_agents"
	aiPortalStateTable           = "aichats_portal_state"
	aiToolApprovalRulesTable     = "aichats_tool_approval_rules"
	aiTranscriptTable            = "aichats_transcript_messages"
	aiCronJobsTable              = "aichats_cron_jobs"
	aiManagedHeartbeatsTable     = "aichats_managed_heartbeats"
	aiCronJobRunKeysTable        = "aichats_cron_job_run_keys"
	aiHeartbeatRunKeysTable      = "aichats_managed_heartbeat_run_keys"
	aiMessageStateTable          = "aichats_message_state"
)

func newBridgeChildDB(parent *dbutil.Database, log zerolog.Logger) *dbutil.Database {
	if parent == nil {
		return nil
	}
	return aidb.NewChild(
		parent,
		dbutil.ZeroLogger(log.With().Str("db_section", "ai").Logger()),
	)
}

func (oc *OpenAIConnector) bridgeDB() *dbutil.Database {
	if oc == nil {
		return nil
	}
	if oc.db != nil {
		return oc.db
	}
	if oc.br != nil && oc.br.DB != nil {
		oc.db = newBridgeChildDB(oc.br.DB.Database, oc.br.Log)
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
		return newBridgeChildDB(oc.UserLogin.Bridge.DB.Database, oc.log)
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
		return newBridgeChildDB(login.Bridge.DB.Database, login.Log)
	}
	return nil
}

func bridgeDBFromPortal(portal *bridgev2.Portal) *dbutil.Database {
	if portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil {
		return nil
	}
	return newBridgeChildDB(portal.Bridge.DB.Database, portal.Bridge.Log)
}

func loginDBContext(client *AIClient) (*dbutil.Database, string, string) {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil {
		return nil, "", ""
	}
	db := client.bridgeDB()
	if db == nil || client.UserLogin.Bridge.DB == nil {
		return nil, "", ""
	}
	return db, string(client.UserLogin.Bridge.DB.BridgeID), string(client.UserLogin.ID)
}
