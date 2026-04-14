package ai

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	bridgev2database "maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/aidb"
)

const (
	aiSessionsTable          = "aichats_sessions"
	aiSystemEventsTable      = "aichats_system_events"
	aiLoginStateTable        = "aichats_login_state"
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

func canonicalBridgeDBID(bridge *bridgev2.Bridge) string {
	if bridge == nil {
		return ""
	}
	if bridge.DB != nil {
		if bridgeID := strings.TrimSpace(string(bridge.DB.BridgeID)); bridgeID != "" {
			return bridgeID
		}
	}
	return strings.TrimSpace(string(bridge.ID))
}

func canonicalLoginBridgeID(login *bridgev2.UserLogin) string {
	if login == nil {
		return ""
	}
	if login.UserLogin != nil {
		if bridgeID := strings.TrimSpace(string(login.UserLogin.BridgeID)); bridgeID != "" {
			return bridgeID
		}
	}
	return canonicalBridgeDBID(login.Bridge)
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
	return canonicalBridgeDBID(portal.Bridge)
}

func normalizePortalDBIdentity(portal *bridgev2.Portal) {
	if portal == nil || portal.Portal == nil {
		return
	}
	if portal.Portal.BridgeID == "" {
		portal.Portal.BridgeID = networkid.BridgeID(canonicalPortalBridgeID(portal))
	}
	if portal.Portal.PortalKey.IsEmpty() && !portal.PortalKey.IsEmpty() {
		portal.Portal.PortalKey = portal.PortalKey
	}
}

func hydratePortalRuntime(target *bridgev2.Portal, hydrated *bridgev2.Portal) *bridgev2.Portal {
	switch {
	case target == nil:
		if hydrated != nil {
			normalizePortalDBIdentity(hydrated)
		}
		return hydrated
	case hydrated == nil || target == hydrated:
		normalizePortalDBIdentity(target)
		return target
	}

	if target.Bridge == nil {
		target.Bridge = hydrated.Bridge
	}
	if hydrated.Bridge == nil {
		hydrated.Bridge = target.Bridge
	}
	if hydrated.Portal != nil {
		target.Portal = hydrated.Portal
	}
	if target.Portal == nil && hydrated.Portal == nil && target.PortalKey != (networkid.PortalKey{}) {
		target.Portal = &bridgev2database.Portal{
			BridgeID:  networkid.BridgeID(canonicalBridgeDBID(target.Bridge)),
			PortalKey: target.PortalKey,
		}
	}
	if hydrated.Parent != nil {
		target.Parent = hydrated.Parent
	}
	if hydrated.Relay != nil {
		target.Relay = hydrated.Relay
	}
	if hydrated.Log.GetLevel() != zerolog.Disabled {
		target.Log = hydrated.Log
	}
	normalizePortalDBIdentity(target)
	return target
}

func resolvePortalForAIDB(ctx context.Context, client *AIClient, portal *bridgev2.Portal) (*bridgev2.Portal, error) {
	if portal == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if client != nil && client.UserLogin != nil && client.UserLogin.Bridge != nil {
		portal.Bridge = client.UserLogin.Bridge
	}
	normalizePortalDBIdentity(portal)
	if scope := portalScopeForPortal(portal); scope != nil {
		return portal, nil
	}
	if portal.Bridge == nil {
		return portal, nil
	}
	if strings.TrimSpace(string(portal.PortalKey.ID)) == "" {
		return portal, nil
	}
	if portal.Bridge.DB != nil {
		dbPortal, err := portal.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
		if err != nil {
			return nil, err
		}
		if dbPortal != nil {
			return hydratePortalRuntime(portal, &bridgev2.Portal{
				Portal: dbPortal,
				Bridge: portal.Bridge,
			}), nil
		}
	}
	resolved, err := portal.Bridge.GetPortalByKey(ctx, portal.PortalKey)
	if err != nil {
		return nil, err
	}
	if resolved != nil {
		resolved = hydratePortalRuntime(portal, resolved)
		if scope := portalScopeForPortal(resolved); scope != nil {
			return resolved, nil
		}
	}
	if scope := portalScopeForPortal(portal); scope != nil {
		return portal, nil
	}
	return hydratePortalRuntime(portal, resolved), nil
}

func resolveAIDBPortalScope(ctx context.Context, client *AIClient, portal *bridgev2.Portal) (*bridgev2.Portal, *portalScope, error) {
	canonicalPortal, err := resolvePortalForAIDB(ctx, client, portal)
	if err != nil || canonicalPortal == nil {
		return nil, nil, err
	}
	return canonicalPortal, portalScopeForPortal(canonicalPortal), nil
}

// loginScope is the shared base for all login-scoped DB access in the AI bridge.
// It contains the database handle plus the bridgeID/loginID pair needed by every
// _db.go file's queries. Embed or use directly instead of defining per-file structs.
type loginScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func (scope *loginScope) ownerKey() string {
	if scope == nil {
		return ""
	}
	return scope.bridgeID + "|" + scope.loginID
}

func (scope *loginScope) sessionStoreRef(agentID string) sessionStoreRef {
	if scope == nil {
		return sessionStoreRef{AgentID: agentID}
	}
	return sessionStoreRef{
		BridgeID: scope.bridgeID,
		LoginID:  scope.loginID,
		AgentID:  agentID,
	}
}

// loginScopeForClient builds a loginScope from an AIClient, returning nil if the
// client is not fully initialised.
func loginScopeForClient(client *AIClient) *loginScope {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil {
		return nil
	}
	db := client.bridgeDB()
	bridgeID := strings.TrimSpace(canonicalLoginBridgeID(client.UserLogin))
	loginID := strings.TrimSpace(canonicalLoginID(client.UserLogin))
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
	loginID := canonicalLoginID(login)
	if strings.TrimSpace(bridgeID) == "" || loginID == "" {
		return nil
	}
	return &loginScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

type portalScopeValueFunc[T any] func(context.Context, *bridgev2.Portal, *portalScope) (T, error)

func withResolvedPortalScopeValue[T any](
	ctx context.Context,
	client *AIClient,
	portal *bridgev2.Portal,
	fn portalScopeValueFunc[T],
) (T, error) {
	var zero T
	if fn == nil {
		return zero, nil
	}
	resolvedPortal, scope, err := resolveAIDBPortalScope(ctx, client, portal)
	if err != nil {
		return zero, err
	}
	return fn(ctx, resolvedPortal, scope)
}

func withResolvedPortalScope(
	ctx context.Context,
	client *AIClient,
	portal *bridgev2.Portal,
	fn func(context.Context, *bridgev2.Portal, *portalScope) error,
) error {
	_, err := withResolvedPortalScopeValue(ctx, client, portal,
		func(ctx context.Context, portal *bridgev2.Portal, scope *portalScope) (struct{}, error) {
			return struct{}{}, fn(ctx, portal, scope)
		},
	)
	return err
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
