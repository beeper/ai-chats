package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/aidb"
)

const (
	aiSessionsTable          = "aichats_sessions"
	aiSystemEventsTable      = "aichats_system_events"
	aiLoginStateTable        = "aichats_login_state"
	aiLoginConfigTable       = "aichats_login_config"
	aiCustomAgentsTable      = "aichats_custom_agents"
	aiPortalStateTable       = "aichats_portal_state"
	aiToolApprovalRulesTable = "aichats_tool_approval_rules"
	aiTurnsTable             = "aichats_turns"
	aiTurnRefsTable          = "aichats_turn_refs"
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

func canonicalLoginBridgeID(login *bridgev2.UserLogin) string {
	if login == nil || login.UserLogin == nil {
		return ""
	}
	return strings.TrimSpace(string(login.UserLogin.BridgeID))
}

func canonicalLoginID(login *bridgev2.UserLogin) string {
	if login == nil {
		return ""
	}
	return strings.TrimSpace(string(login.ID))
}

func canonicalPortalBridgeID(portal *bridgev2.Portal) string {
	if portal == nil {
		return ""
	}
	if portal.Portal != nil {
		if bridgeID := strings.TrimSpace(string(portal.Portal.BridgeID)); bridgeID != "" {
			return bridgeID
		}
	}
	if portal.Bridge != nil {
		return strings.TrimSpace(string(portal.Bridge.ID))
	}
	return ""
}

func canonicalPortalForAIDB(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.Portal, error) {
	if portal == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if scope := portalScopeForPortal(portal); scope != nil {
		return portal, nil
	}
	if portal.Bridge == nil {
		return portal, nil
	}
	if portal.Bridge.DB != nil {
		dbPortal, err := portal.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
		if err != nil {
			return nil, err
		}
		if dbPortal != nil {
			portal.Portal = dbPortal
			return portal, nil
		}
	}
	resolved, err := portal.Bridge.GetPortalByKey(ctx, portal.PortalKey)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func portalScopeForAIDB(ctx context.Context, portal *bridgev2.Portal) (*portalScope, error) {
	canonicalPortal, err := canonicalPortalForAIDB(ctx, portal)
	if err != nil || canonicalPortal == nil {
		return nil, err
	}
	return portalScopeForPortal(canonicalPortal), nil
}

func loginDBContext(client *AIClient) (*dbutil.Database, string, string) {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil {
		return nil, "", ""
	}
	db := client.bridgeDB()
	bridgeID := canonicalLoginBridgeID(client.UserLogin)
	loginID := strings.TrimSpace(string(client.UserLogin.ID))
	if db == nil || bridgeID == "" || loginID == "" {
		return nil, "", ""
	}
	return db, bridgeID, loginID
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
	if db == nil {
		return nil
	}
	bridgeID := canonicalLoginBridgeID(login)
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &loginScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

// unmarshalJSONField unmarshals a JSON string into *T, returning nil when the
// input is empty. This replaces the repeated "if TrimSpace != "" { Unmarshal }" blocks.
func unmarshalJSONField[T any](raw string) (*T, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out T
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// unmarshalMapJSONField unmarshals a JSON string into map[K]V, returning nil when empty.
func unmarshalMapJSONField[K comparable, V any](raw string) (map[K]V, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out map[K]V
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

type portalScope struct {
	db             *dbutil.Database
	bridgeID       string
	portalID       string
	portalReceiver string
}

func portalScopeForPortal(portal *bridgev2.Portal) *portalScope {
	db := bridgeDBFromPortal(portal)
	if db == nil || portal == nil {
		return nil
	}
	bridgeID := canonicalPortalBridgeID(portal)
	portalID := strings.TrimSpace(string(portal.PortalKey.ID))
	portalReceiver := strings.TrimSpace(string(portal.PortalKey.Receiver))
	if bridgeID == "" || portalID == "" || portalReceiver == "" {
		return nil
	}
	return &portalScope{
		db:             db,
		bridgeID:       bridgeID,
		portalID:       portalID,
		portalReceiver: portalReceiver,
	}
}
