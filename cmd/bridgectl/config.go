package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func patchConfigWithRegistration(configPath string, reg any, homeserverURL, bridgeName, bridgeType, beeperDomain, asToken, userID, provisioningSecret string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	regMap := toMap(reg)

	// Homeserver -- hungryserv websocket mode
	setPath(doc, []string{"homeserver", "address"}, homeserverURL)
	setPath(doc, []string{"homeserver", "domain"}, "beeper.local")
	setPath(doc, []string{"homeserver", "software"}, "hungry")
	setPath(doc, []string{"homeserver", "async_media"}, true)
	setPath(doc, []string{"homeserver", "websocket"}, true)
	setPath(doc, []string{"homeserver", "ping_interval_seconds"}, 180)

	// Appservice -- registration tokens
	setPath(doc, []string{"appservice", "address"}, "irrelevant")
	setPath(doc, []string{"appservice", "as_token"}, regMap["as_token"])
	setPath(doc, []string{"appservice", "hs_token"}, regMap["hs_token"])
	if v, ok := regMap["id"]; ok {
		setPath(doc, []string{"appservice", "id"}, v)
	}
	if v, ok := regMap["sender_localpart"]; ok {
		if s, ok2 := v.(string); ok2 {
			setPath(doc, []string{"appservice", "bot", "username"}, s)
		}
	}
	setPath(doc, []string{"appservice", "username_template"}, fmt.Sprintf("%s_{{.}}", bridgeName))

	// Bridge -- Beeper defaults
	setPath(doc, []string{"bridge", "personal_filtering_spaces"}, true)
	setPath(doc, []string{"bridge", "private_chat_portal_meta"}, false)
	setPath(doc, []string{"bridge", "split_portals"}, true)
	setPath(doc, []string{"bridge", "bridge_status_notices"}, "none")
	setPath(doc, []string{"bridge", "cross_room_replies"}, true)
	setPath(doc, []string{"bridge", "cleanup_on_logout", "enabled"}, true)
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "private"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "relayed"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_no_users"}, "delete")
	setPath(doc, []string{"bridge", "cleanup_on_logout", "manual", "shared_has_users"}, "delete")
	setPath(doc, []string{"bridge", "permissions", userID}, "admin")

	// Database -- sqlite for self-hosted
	setPath(doc, []string{"database", "type"}, "sqlite3-fk-wal")
	setPath(doc, []string{"database", "uri"}, "file:ai.db?_txlock=immediate")

	// Matrix connector
	setPath(doc, []string{"matrix", "message_status_events"}, true)
	setPath(doc, []string{"matrix", "message_error_notices"}, false)
	setPath(doc, []string{"matrix", "sync_direct_chat_list"}, false)
	setPath(doc, []string{"matrix", "federate_rooms"}, false)

	// Provisioning
	if provisioningSecret != "" {
		setPath(doc, []string{"provisioning", "shared_secret"}, provisioningSecret)
	}
	setPath(doc, []string{"provisioning", "allow_matrix_auth"}, true)
	setPath(doc, []string{"provisioning", "debug_endpoints"}, true)

	// Double puppet -- allow beeper.com users
	setPath(doc, []string{"double_puppet", "servers", beeperDomain}, homeserverURL)
	setPath(doc, []string{"double_puppet", "secrets", beeperDomain}, "as_token:"+asToken)
	setPath(doc, []string{"double_puppet", "allow_discovery"}, false)

	// Backfill
	setPath(doc, []string{"backfill", "enabled"}, true)
	setPath(doc, []string{"backfill", "queue", "enabled"}, true)
	setPath(doc, []string{"backfill", "queue", "batch_size"}, 50)
	setPath(doc, []string{"backfill", "queue", "max_batches"}, 0)

	// Encryption -- end-to-bridge encryption for Beeper
	setPath(doc, []string{"encryption", "allow"}, true)
	setPath(doc, []string{"encryption", "default"}, true)
	setPath(doc, []string{"encryption", "require"}, true)
	setPath(doc, []string{"encryption", "appservice"}, true)
	setPath(doc, []string{"encryption", "allow_key_sharing"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_outbound_on_ack"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "ratchet_on_decrypt"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_fully_used_on_decrypt"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_prev_on_new_session"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "delete_on_device_delete"}, true)
	setPath(doc, []string{"encryption", "delete_keys", "periodically_delete_expired"}, true)
	setPath(doc, []string{"encryption", "verification_levels", "receive"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "verification_levels", "send"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "verification_levels", "share"}, "cross-signed-tofu")
	setPath(doc, []string{"encryption", "rotation", "enable_custom"}, true)
	setPath(doc, []string{"encryption", "rotation", "milliseconds"}, 2592000000)
	setPath(doc, []string{"encryption", "rotation", "messages"}, 10000)
	setPath(doc, []string{"encryption", "rotation", "disable_device_change_key_rotation"}, true)

	// Network
	if bridgeType != "" {
		setPath(doc, []string{"network", "bridge_type"}, bridgeType)
	}

	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

func applyConfigOverrides(configPath string, overrides map[string]any) error {
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
		setPath(doc, strings.Split(k, "."), v)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o600)
}

func setPath(root map[string]any, parts []string, value any) {
	if len(parts) == 0 {
		return
	}
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		next, ok := cur[key]
		if !ok {
			nm := map[string]any{}
			cur[key] = nm
			cur = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			nm = map[string]any{}
			cur[key] = nm
		}
		cur = nm
	}
	cur[parts[len(parts)-1]] = value
}

func getDatabaseURI(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}
	dbRaw, ok := doc["database"]
	if !ok {
		return "", nil
	}
	dbMap, ok := dbRaw.(map[string]any)
	if !ok {
		return "", nil
	}
	uriRaw, ok := dbMap["uri"]
	if !ok {
		return "", nil
	}
	uri, ok := uriRaw.(string)
	if !ok {
		return "", nil
	}
	return uri, nil
}

func toMap(v any) map[string]any {
	data, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}
