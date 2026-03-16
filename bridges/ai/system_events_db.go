package ai

import (
	"context"
	"slices"
	"strings"

	"go.mau.fi/util/dbutil"

	"github.com/beeper/agentremote/pkg/agents"
)

type persistedSystemEventQueue struct {
	AgentID    string
	SessionKey string
	Events     []SystemEvent
	LastText   string
}

type systemEventsDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
	agentID  string
}

func normalizeSystemEventsAgentID(agentID string) string {
	normalized := normalizeAgentID(agentID)
	if normalized == "" {
		return "beeper"
	}
	return normalized
}

func systemEventsScope(client *AIClient, agentID string) *systemEventsDBScope {
	db, bridgeID, loginID := loginDBContext(client)
	if db == nil {
		return nil
	}
	return &systemEventsDBScope{
		db:       db,
		bridgeID: bridgeID,
		loginID:  loginID,
		agentID:  normalizeSystemEventsAgentID(agentID),
	}
}

func (scope *systemEventsDBScope) ownerKey() string {
	if scope == nil {
		return ""
	}
	return scope.bridgeID + "|" + scope.loginID
}

func snapshotSystemEvents(ownerKey string) []persistedSystemEventQueue {
	systemEventsMu.Lock()
	defer systemEventsMu.Unlock()

	snap := make([]persistedSystemEventQueue, 0, len(systemEvents))
	for key, entry := range systemEvents {
		owner, sessionKey, ok := splitSystemEventsMapKey(key)
		if !ok || owner != strings.TrimSpace(ownerKey) {
			continue
		}
		if entry == nil || len(entry.queue) == 0 {
			continue
		}
		snap = append(snap, persistedSystemEventQueue{
			AgentID:    normalizeSystemEventsAgentID(entry.lastContextKey),
			SessionKey: sessionKey,
			Events:     slices.Clone(entry.queue),
			LastText:   entry.lastText,
		})
	}
	return snap
}

func persistSystemEventsSnapshot(client *AIClient) {
	baseScope := systemEventsScope(client, agents.DefaultAgentID)
	if baseScope == nil {
		return
	}
	grouped := make(map[string][]persistedSystemEventQueue)
	for _, queue := range snapshotSystemEvents(baseScope.ownerKey()) {
		agentID := normalizeSystemEventsAgentID(queue.AgentID)
		queue.AgentID = agentID
		grouped[agentID] = append(grouped[agentID], queue)
	}
	existingAgentIDs, err := listPersistedSystemEventAgentIDs(context.Background(), baseScope)
	if err == nil {
		for _, agentID := range existingAgentIDs {
			if _, ok := grouped[agentID]; !ok {
				grouped[agentID] = nil
			}
		}
	}
	for agentID, queues := range grouped {
		if err := saveSystemEventsSnapshot(context.Background(), systemEventsScope(client, agentID), queues); err != nil {
			if log := client.Log(); log != nil {
				log.Warn().Err(err).Str("agent_id", agentID).Msg("system events: write failed during persist")
			}
			return
		}
	}
	if err != nil {
		if log := client.Log(); log != nil {
			log.Warn().Err(err).Msg("system events: write failed during persist")
		}
	}
}

func restoreSystemEventsFromDB(client *AIClient) {
	baseScope := systemEventsScope(client, agents.DefaultAgentID)
	if baseScope == nil {
		return
	}
	agentIDs, err := listPersistedSystemEventAgentIDs(context.Background(), baseScope)
	if err != nil {
		if log := client.Log(); log != nil {
			log.Warn().Err(err).Msg("system events: read failed during restore")
		}
		return
	}
	for _, agentID := range agentIDs {
		scope := systemEventsScope(client, agentID)
		queues, loadErr := loadSystemEventsSnapshot(context.Background(), scope)
		if loadErr != nil {
			if log := client.Log(); log != nil {
				log.Warn().Err(loadErr).Str("agent_id", agentID).Msg("system events: read failed during restore")
			}
			continue
		}
		systemEventsMu.Lock()
		for _, queue := range queues {
			if strings.TrimSpace(queue.SessionKey) == "" || len(queue.Events) == 0 {
				continue
			}
			mapKey, err := buildSystemEventsMapKey(scope.ownerKey(), queue.SessionKey)
			if err != nil {
				continue
			}
			existing := systemEvents[mapKey]
			if existing != nil && len(existing.queue) > 0 {
				continue
			}
			systemEvents[mapKey] = &systemEventQueue{
				queue:          slices.Clone(queue.Events),
				lastText:       queue.LastText,
				lastContextKey: agentID,
			}
		}
		systemEventsMu.Unlock()
	}
}

func listPersistedSystemEventAgentIDs(ctx context.Context, scope *systemEventsDBScope) ([]string, error) {
	if scope == nil {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT DISTINCT agent_id
		FROM aichats_system_events
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY agent_id
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agentIDs []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, err
		}
		agentIDs = append(agentIDs, normalizeSystemEventsAgentID(agentID))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return agentIDs, nil
}

func saveSystemEventsSnapshot(ctx context.Context, scope *systemEventsDBScope, queues []persistedSystemEventQueue) error {
	if scope == nil {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `DELETE FROM aichats_system_events WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3`, scope.bridgeID, scope.loginID, scope.agentID); err != nil {
			return err
		}
		for _, queue := range queues {
			if strings.TrimSpace(queue.SessionKey) == "" {
				continue
			}
			for idx, evt := range queue.Events {
				lastText := ""
				if idx == len(queue.Events)-1 {
					lastText = queue.LastText
				}
				if _, err := scope.db.Exec(ctx, `
					INSERT INTO aichats_system_events (
						bridge_id, login_id, agent_id, session_key, event_index, text, ts, last_text
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
				`, scope.bridgeID, scope.loginID, scope.agentID, queue.SessionKey, idx, evt.Text, evt.TS, lastText); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func loadSystemEventsSnapshot(ctx context.Context, scope *systemEventsDBScope) ([]persistedSystemEventQueue, error) {
	if scope == nil {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT session_key, text, ts, last_text
		FROM aichats_system_events
		WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3
		ORDER BY session_key, event_index
	`, scope.bridgeID, scope.loginID, scope.agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queues []persistedSystemEventQueue
	var current *persistedSystemEventQueue
	for rows.Next() {
		var (
			sessionKey string
			text       string
			ts         int64
			lastText   string
		)
		if err := rows.Scan(&sessionKey, &text, &ts, &lastText); err != nil {
			return nil, err
		}
		if current == nil || current.SessionKey != sessionKey {
			queues = append(queues, persistedSystemEventQueue{SessionKey: sessionKey})
			current = &queues[len(queues)-1]
		}
		current.Events = append(current.Events, SystemEvent{Text: text, TS: ts})
		if strings.TrimSpace(lastText) != "" {
			current.LastText = lastText
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return queues, nil
}
