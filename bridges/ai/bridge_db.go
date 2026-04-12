package ai

import (
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/aidb"
)

const (
	aiSessionsTable          = "aichats_sessions"
	aiSystemEventsTable      = "aichats_system_events"
	aiInternalMessagesTable  = "aichats_internal_messages"
	aiLoginStateTable        = "aichats_login_state"
	aiLoginConfigTable       = "aichats_login_config"
	aiCustomAgentsTable      = "aichats_custom_agents"
	aiPortalStateTable       = "aichats_portal_state"
	aiToolApprovalRulesTable = "aichats_tool_approval_rules"
	aiTranscriptTable        = "aichats_transcript_messages"
	aiCronJobsTable          = "aichats_cron_jobs"
	aiManagedHeartbeatsTable = "aichats_managed_heartbeats"
	aiCronJobRunKeysTable    = "aichats_cron_job_run_keys"
	aiHeartbeatRunKeysTable  = "aichats_managed_heartbeat_run_keys"
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

// loginScope is the shared base for all login-scoped DB access in the AI bridge.
// It contains the database handle plus the bridgeID/loginID pair needed by every
// _db.go file's queries. Embed or use directly instead of defining per-file structs.
type loginScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

// loginScopeForClient builds a loginScope from an AIClient, returning nil if the
// client is not fully initialised.
func loginScopeForClient(client *AIClient) *loginScope {
	db, bridgeID, loginID := loginDBContext(client)
	bridgeID = strings.TrimSpace(bridgeID)
	loginID = strings.TrimSpace(loginID)
	if db == nil || bridgeID == "" || loginID == "" {
		return nil
	}
	return &loginScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

// loginScopeForLogin builds a loginScope from a UserLogin, returning nil if the
// login or its database is not available.
func loginScopeForLogin(login *bridgev2.UserLogin) *loginScope {
	db := bridgeDBFromLogin(login)
	if db == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &loginScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

// portalScope extends loginScope with a portal identifier for portal-scoped DB tables.
type portalScope struct {
	*loginScope
	portalID string
}

// portalScopeForPortal builds a portalScope from a Portal, returning nil if
// the portal or its database is not available.
func portalScopeForPortal(portal *bridgev2.Portal) *portalScope {
	db := bridgeDBFromPortal(portal)
	if db == nil || portal.Bridge == nil || portal.Bridge.DB == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(portal.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(portal.Receiver))
	portalID := strings.TrimSpace(string(portal.PortalKey.ID))
	if bridgeID == "" || loginID == "" || portalID == "" {
		return nil
	}
	return &portalScope{
		loginScope: &loginScope{db: db, bridgeID: bridgeID, loginID: loginID},
		portalID:   portalID,
	}
}
