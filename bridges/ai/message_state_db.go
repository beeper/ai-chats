package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

type aiMessageState struct {
	Body              string             `json:"body,omitempty"`
	CanonicalTurnData map[string]any     `json:"canonical_turn_data,omitempty"`
	GeneratedFiles    []GeneratedFileRef `json:"generated_files,omitempty"`
}

type messageStateScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func messageStateScopeForClient(client *AIClient) *messageStateScope {
	db, bridgeID, loginID := loginDBContext(client)
	if db == nil || strings.TrimSpace(bridgeID) == "" || strings.TrimSpace(loginID) == "" {
		return nil
	}
	return &messageStateScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

func cloneAIMessageState(src *aiMessageState) *aiMessageState {
	if src == nil {
		return &aiMessageState{}
	}
	data, err := json.Marshal(src)
	if err != nil {
		return &aiMessageState{
			Body:           src.Body,
			GeneratedFiles: append([]GeneratedFileRef(nil), src.GeneratedFiles...),
		}
	}
	var clone aiMessageState
	if err = json.Unmarshal(data, &clone); err != nil {
		return &aiMessageState{
			Body:           src.Body,
			GeneratedFiles: append([]GeneratedFileRef(nil), src.GeneratedFiles...),
		}
	}
	return &clone
}

func cloneCanonicalTurnData(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if err = json.Unmarshal(data, &clone); err != nil {
		return nil
	}
	return clone
}

func cloneMessageMetadata(src *MessageMetadata) *MessageMetadata {
	if src == nil {
		return nil
	}
	data, err := json.Marshal(src)
	if err != nil {
		clone := &MessageMetadata{}
		clone.CopyFrom(src)
		clone.MediaUnderstanding = append([]MediaUnderstandingOutput(nil), src.MediaUnderstanding...)
		clone.MediaUnderstandingDecisions = append([]MediaUnderstandingDecision(nil), src.MediaUnderstandingDecisions...)
		clone.MediaURL = src.MediaURL
		clone.MimeType = src.MimeType
		return clone
	}
	var clone MessageMetadata
	if err = json.Unmarshal(data, &clone); err != nil {
		fallback := &MessageMetadata{}
		fallback.CopyFrom(src)
		fallback.MediaUnderstanding = append([]MediaUnderstandingOutput(nil), src.MediaUnderstanding...)
		fallback.MediaUnderstandingDecisions = append([]MediaUnderstandingDecision(nil), src.MediaUnderstandingDecisions...)
		fallback.MediaURL = src.MediaURL
		fallback.MimeType = src.MimeType
		return fallback
	}
	return &clone
}

func cloneMessageForAIHistory(msg *database.Message) *database.Message {
	if msg == nil {
		return nil
	}
	clone := *msg
	if meta, ok := msg.Metadata.(*MessageMetadata); ok {
		clone.Metadata = cloneMessageMetadata(meta)
	}
	return &clone
}

func loadAIMessageState(ctx context.Context, client *AIClient, roomID id.RoomID, messageID networkid.MessageID) (*aiMessageState, error) {
	if strings.TrimSpace(string(messageID)) == "" {
		return nil, nil
	}
	states, err := loadAIMessageStates(ctx, client, roomID, []networkid.MessageID{messageID})
	if err != nil {
		return nil, err
	}
	return states[string(messageID)], nil
}

func loadAIMessageStates(ctx context.Context, client *AIClient, roomID id.RoomID, messageIDs []networkid.MessageID) (map[string]*aiMessageState, error) {
	scope := messageStateScopeForClient(client)
	if scope == nil || roomID == "" || len(messageIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, 3+len(messageIDs))
	args = append(args, scope.bridgeID, scope.loginID, roomID.String())
	placeholders := make([]string, 0, len(messageIDs))
	for i, messageID := range messageIDs {
		if strings.TrimSpace(string(messageID)) == "" {
			continue
		}
		args = append(args, string(messageID))
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+4))
	}
	if len(placeholders) == 0 {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT message_id, state_json
		FROM `+aiMessageStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND room_id=$3 AND message_id IN (`+strings.Join(placeholders, ", ")+`)
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]*aiMessageState, len(placeholders))
	for rows.Next() {
		var messageID string
		var raw string
		if err = rows.Scan(&messageID, &raw); err != nil {
			return nil, err
		}
		if strings.TrimSpace(messageID) == "" || strings.TrimSpace(raw) == "" {
			continue
		}
		var state aiMessageState
		if err = json.Unmarshal([]byte(raw), &state); err != nil {
			return nil, err
		}
		out[messageID] = &state
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func saveAIMessageState(ctx context.Context, client *AIClient, roomID id.RoomID, messageID networkid.MessageID, state *aiMessageState) error {
	scope := messageStateScopeForClient(client)
	if scope == nil || roomID == "" || strings.TrimSpace(string(messageID)) == "" || state == nil {
		return nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiMessageStateTable+` (
			bridge_id, login_id, room_id, message_id, state_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (bridge_id, login_id, room_id, message_id) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, scope.bridgeID, scope.loginID, roomID.String(), string(messageID), string(payload), time.Now().UnixMilli())
	return err
}

func applyAIMessageState(meta *MessageMetadata, state *aiMessageState) {
	if meta == nil || state == nil {
		return
	}
	if state.Body != "" {
		meta.Body = state.Body
	}
	if len(state.CanonicalTurnData) > 0 {
		data, err := json.Marshal(state.CanonicalTurnData)
		if err == nil {
			var clone map[string]any
			if json.Unmarshal(data, &clone) == nil {
				meta.CanonicalTurnData = clone
			}
		}
	}
	if len(state.GeneratedFiles) > 0 {
		meta.GeneratedFiles = append([]GeneratedFileRef(nil), state.GeneratedFiles...)
	}
}

func (oc *AIClient) applyAIMessageStates(ctx context.Context, portal *bridgev2.Portal, messages []*database.Message) ([]*database.Message, error) {
	if oc == nil || portal == nil || portal.MXID == "" || len(messages) == 0 {
		return messages, nil
	}
	ids := make([]networkid.MessageID, 0, len(messages))
	for _, msg := range messages {
		if msg != nil && msg.ID != "" {
			ids = append(ids, msg.ID)
		}
	}
	states, err := loadAIMessageStates(ctx, oc, portal.MXID, ids)
	if err != nil || len(states) == 0 {
		return messages, err
	}
	out := make([]*database.Message, len(messages))
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		state := states[string(msg.ID)]
		if state == nil {
			out[i] = msg
			continue
		}
		clone := cloneMessageForAIHistory(msg)
		if meta, ok := clone.Metadata.(*MessageMetadata); ok && meta != nil {
			applyAIMessageState(meta, state)
		}
		out[i] = clone
	}
	return out, nil
}

func (oc *AIClient) getAIHistoryMessages(ctx context.Context, portal *bridgev2.Portal, limit int) ([]*database.Message, error) {
	if oc == nil || portal == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil || oc.UserLogin.Bridge.DB.Message == nil {
		return nil, nil
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, limit)
	if err != nil {
		return nil, err
	}
	return oc.applyAIMessageStates(ctx, portal, messages)
}
