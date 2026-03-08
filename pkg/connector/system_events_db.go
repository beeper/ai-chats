package connector

import (
	"context"
	"slices"
	"strings"

	"go.mau.fi/util/dbutil"
)

type persistedSystemEventQueue struct {
	SessionKey string
	Events     []SystemEvent
	LastText   string
}

type systemEventsDBScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func systemEventsScope(client *AIClient) *systemEventsDBScope {
	if client == nil || client.UserLogin == nil || client.UserLogin.Bridge == nil {
		return nil
	}
	db := client.bridgeDB()
	if db == nil || client.UserLogin.Bridge.DB == nil {
		return nil
	}
	return &systemEventsDBScope{
		db:       db,
		bridgeID: string(client.UserLogin.Bridge.DB.BridgeID),
		loginID:  string(client.UserLogin.ID),
	}
}

func (scope *systemEventsDBScope) ownerKey() string {
	if scope == nil {
		return ""
	}
	return scope.bridgeID + systemEventsKeySeparator + scope.loginID
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
			SessionKey: sessionKey,
			Events:     slices.Clone(entry.queue),
			LastText:   entry.lastText,
		})
	}
	return snap
}

func persistSystemEventsSnapshot(client *AIClient) {
	scope := systemEventsScope(client)
	if scope == nil {
		return
	}
	if err := saveSystemEventsSnapshot(context.Background(), scope, snapshotSystemEvents(scope.ownerKey())); err != nil {
		if log := client.Log(); log != nil {
			log.Warn().Err(err).Msg("system events: write failed during persist")
		}
	}
}

func restoreSystemEventsFromDB(client *AIClient) {
	scope := systemEventsScope(client)
	if scope == nil {
		return
	}
	queues, err := loadSystemEventsSnapshot(context.Background(), scope)
	if err != nil {
		if log := client.Log(); log != nil {
			log.Warn().Err(err).Msg("system events: read failed during restore")
		}
		return
	}
	systemEventsMu.Lock()
	defer systemEventsMu.Unlock()
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
			queue:    slices.Clone(queue.Events),
			lastText: queue.LastText,
		}
	}
}

func saveSystemEventsSnapshot(ctx context.Context, scope *systemEventsDBScope, queues []persistedSystemEventQueue) error {
	if scope == nil {
		return nil
	}
	return scope.db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := scope.db.Exec(ctx, `DELETE FROM ai_system_events WHERE bridge_id=$1 AND login_id=$2`, scope.bridgeID, scope.loginID); err != nil {
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
					INSERT INTO ai_system_events (
						bridge_id, login_id, session_key, event_index, text, ts, last_text
					) VALUES ($1, $2, $3, $4, $5, $6, $7)
				`, scope.bridgeID, scope.loginID, queue.SessionKey, idx, evt.Text, evt.TS, lastText); err != nil {
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
		SELECT session_key, event_index, text, ts, last_text
		FROM ai_system_events
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY session_key, event_index
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queues []persistedSystemEventQueue
	var current *persistedSystemEventQueue
	for rows.Next() {
		var (
			sessionKey string
			index      int
			text       string
			ts         int64
			lastText   string
		)
		if err := rows.Scan(&sessionKey, &index, &text, &ts, &lastText); err != nil {
			return nil, err
		}
		if current == nil || current.SessionKey != sessionKey {
			queues = append(queues, persistedSystemEventQueue{SessionKey: sessionKey})
			current = &queues[len(queues)-1]
		}
		_ = index
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
