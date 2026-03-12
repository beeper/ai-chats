package store

import (
	"context"
	"strings"
)

type SystemEvent struct {
	Text string
	TS   int64
}

type SystemEventQueue struct {
	SessionKey string
	Events     []SystemEvent
	LastText   string
}

type SystemEventStore struct {
	scope *Scope
}

func (s *SystemEventStore) Replace(ctx context.Context, queues []SystemEventQueue) error {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return nil
	}
	return s.scope.DB.DoTxn(ctx, nil, func(ctx context.Context) error {
		if _, err := s.scope.DB.Exec(ctx, `DELETE FROM ai_system_events WHERE bridge_id=$1 AND login_id=$2`, s.scope.BridgeID, s.scope.LoginID); err != nil {
			return err
		}
		for _, queue := range queues {
			sessionKey := strings.TrimSpace(queue.SessionKey)
			if sessionKey == "" {
				continue
			}
			for idx, evt := range queue.Events {
				lastText := ""
				if idx == len(queue.Events)-1 {
					lastText = queue.LastText
				}
				if _, err := s.scope.DB.Exec(ctx, `
					INSERT INTO ai_system_events (
						bridge_id, login_id, session_key, event_index, text, ts, last_text
					) VALUES ($1, $2, $3, $4, $5, $6, $7)
				`, s.scope.BridgeID, s.scope.LoginID, sessionKey, idx, evt.Text, evt.TS, lastText); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *SystemEventStore) Load(ctx context.Context) ([]SystemEventQueue, error) {
	if s == nil || s.scope == nil || s.scope.DB == nil {
		return nil, nil
	}
	rows, err := s.scope.DB.Query(ctx, `
		SELECT session_key, event_index, text, ts, last_text
		FROM ai_system_events
		WHERE bridge_id=$1 AND login_id=$2
		ORDER BY session_key, event_index
	`, s.scope.BridgeID, s.scope.LoginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queues []SystemEventQueue
	var current *SystemEventQueue
	for rows.Next() {
		var (
			sessionKey string
			eventIndex int
			text       string
			ts         int64
			lastText   string
		)
		if err := rows.Scan(&sessionKey, &eventIndex, &text, &ts, &lastText); err != nil {
			return nil, err
		}
		_ = eventIndex
		if current == nil || current.SessionKey != sessionKey {
			queues = append(queues, SystemEventQueue{SessionKey: sessionKey})
			current = &queues[len(queues)-1]
		}
		current.Events = append(current.Events, SystemEvent{Text: text, TS: ts})
		if strings.TrimSpace(lastText) != "" {
			current.LastText = lastText
		}
	}
	return queues, rows.Err()
}
