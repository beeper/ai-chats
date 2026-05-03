package bridgeutil

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/beeper/ai-chats/pkg/shared/jsonutil"
)

// PatchConfigWithRegistration applies the standard Beeper self-hosted bridge
// configuration to the YAML config file at configPath. It merges homeserver,
// appservice, bridge, database, matrix, provisioning, encryption and other
// sections required for websocket-mode operation against hungryserv.
func PatchConfigWithRegistration(configPath string, reg any, homeserverURL, bridgeName, bridgeType, dbName, beeperDomain, asToken, userID, matrixToken, provisioningSecret string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	regMap := jsonutil.ToMap(reg)

	// Homeserver — hungryserv websocket mode
	SetPath(doc, []string{"homeserver", "address"}, homeserverURL)
	SetPath(doc, []string{"homeserver", "domain"}, "beeper.local")
	SetPath(doc, []string{"homeserver", "software"}, "hungry")
	SetPath(doc, []string{"homeserver", "async_media"}, true)
	SetPath(doc, []string{"homeserver", "websocket"}, true)
	SetPath(doc, []string{"homeserver", "ping_interval_seconds"}, 180)

	// Appservice — registration tokens
	SetPath(doc, []string{"appservice", "address"}, "irrelevant")
	SetPath(doc, []string{"appservice", "as_token"}, regMap["as_token"])
	SetPath(doc, []string{"appservice", "hs_token"}, regMap["hs_token"])
	if v, ok := regMap["id"]; ok {
		SetPath(doc, []string{"appservice", "id"}, v)
	}
	if v, ok := regMap["sender_localpart"]; ok {
		if s, ok2 := v.(string); ok2 {
			SetPath(doc, []string{"appservice", "bot", "username"}, s)
		}
	}
	SetPath(doc, []string{"appservice", "username_template"}, fmt.Sprintf("%s_{{.}}", bridgeName))

	// Bridge — Beeper defaults
	SetPath(doc, []string{"bridge", "personal_filtering_spaces"}, true)
	SetPath(doc, []string{"bridge", "private_chat_portal_meta"}, false)
	SetPath(doc, []string{"bridge", "split_portals"}, true)
	SetPath(doc, []string{"bridge", "bridge_status_notices"}, "none")
	SetPath(doc, []string{"bridge", "cross_room_replies"}, true)
	SetPath(doc, []string{"bridge", "cleanup_on_logout", "enabled"}, true)
	SetPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "private"}, "delete")
	SetPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "relayed"}, "delete")
	SetPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_no_users"}, "delete")
	SetPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_has_users"}, "delete")
	SetPath(doc, []string{"bridge", "permissions", userID}, "admin")

	// Database — sqlite for self-hosted
	SetPath(doc, []string{"database", "type"}, "sqlite3-fk-wal")
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		dbName = "ai.db"
	}
	SetPath(doc, []string{"database", "uri"}, fmt.Sprintf("file:%s?_txlock=immediate", dbName))

	// Matrix connector
	SetPath(doc, []string{"matrix", "message_status_events"}, true)
	SetPath(doc, []string{"matrix", "message_error_notices"}, false)
	SetPath(doc, []string{"matrix", "sync_direct_chat_list"}, false)
	SetPath(doc, []string{"matrix", "federate_rooms"}, false)

	// Provisioning
	if provisioningSecret != "" {
		SetPath(doc, []string{"provisioning", "shared_secret"}, provisioningSecret)
	}
	SetPath(doc, []string{"provisioning", "allow_matrix_auth"}, true)
	SetPath(doc, []string{"provisioning", "debug_endpoints"}, true)

	// Managed Beeper Cloud auth
	SetPath(doc, []string{"network", "beeper", "user_mxid"}, userID)
	SetPath(doc, []string{"network", "beeper", "base_url"}, homeserverURL)
	SetPath(doc, []string{"network", "beeper", "token"}, matrixToken)

	// Double puppet — allow beeper.com users
	SetPath(doc, []string{"double_puppet", "servers", beeperDomain}, homeserverURL)
	SetPath(doc, []string{"double_puppet", "secrets", beeperDomain}, "as_token:"+asToken)
	SetPath(doc, []string{"double_puppet", "allow_discovery"}, false)

	// Backfill
	SetPath(doc, []string{"backfill", "enabled"}, true)
	SetPath(doc, []string{"backfill", "queue", "enabled"}, true)
	SetPath(doc, []string{"backfill", "queue", "batch_size"}, 50)
	SetPath(doc, []string{"backfill", "queue", "max_batches"}, 0)

	// Encryption — end-to-bridge encryption for Beeper
	SetPath(doc, []string{"encryption", "allow"}, true)
	SetPath(doc, []string{"encryption", "default"}, true)
	SetPath(doc, []string{"encryption", "require"}, true)
	SetPath(doc, []string{"encryption", "appservice"}, true)
	SetPath(doc, []string{"encryption", "allow_key_sharing"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "delete_outbound_on_ack"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "ratchet_on_decrypt"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "delete_fully_used_on_decrypt"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "delete_prev_on_new_session"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "delete_on_device_delete"}, true)
	SetPath(doc, []string{"encryption", "delete_keys", "periodically_delete_expired"}, true)
	SetPath(doc, []string{"encryption", "verification_levels", "receive"}, "cross-signed-tofu")
	SetPath(doc, []string{"encryption", "verification_levels", "send"}, "cross-signed-tofu")
	SetPath(doc, []string{"encryption", "verification_levels", "share"}, "cross-signed-tofu")
	SetPath(doc, []string{"encryption", "rotation", "enable_custom"}, true)
	SetPath(doc, []string{"encryption", "rotation", "milliseconds"}, 2592000000)
	SetPath(doc, []string{"encryption", "rotation", "messages"}, 10000)
	SetPath(doc, []string{"encryption", "rotation", "disable_device_change_key_rotation"}, true)

	// Network
	if bridgeType != "" {
		SetPath(doc, []string{"network", "bridge_type"}, bridgeType)
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

// ApplyConfigOverrides reads a YAML config file at configPath, applies the
// given dot-separated key overrides, and writes the result back.
func ApplyConfigOverrides(configPath string, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	for k, v := range overrides {
		parts := strings.Split(k, ".")
		SetPath(doc, parts, v)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

// SetPath sets a nested value inside a map[string]any tree, creating
// intermediate maps as needed. For example, SetPath(doc, ["a","b","c"], 42)
// ensures doc["a"]["b"]["c"] == 42.
func SetPath(root map[string]any, parts []string, value any) {
	if len(parts) == 0 {
		return
	}
	cur := root
	for i := range len(parts) - 1 {
		key := parts[i]
		nm, ok := cur[key].(map[string]any)
		if !ok {
			nm = map[string]any{}
			cur[key] = nm
		}
		cur = nm
	}
	cur[parts[len(parts)-1]] = value
}
