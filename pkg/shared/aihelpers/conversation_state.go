package aihelpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

type aiConversationState struct {
	Kind                 ConversationKind
	Visibility           ConversationVisibility
	ParentConversationID string
	ParentEventID        string
	ArchiveOnCompletion  bool
	Metadata             map[string]any
	RoomAgents           RoomAgentSet
}

func (s *aiConversationState) clone() *aiConversationState {
	if s == nil {
		return &aiConversationState{}
	}
	out := *s
	out.Metadata = maps.Clone(s.Metadata)
	out.RoomAgents.AgentIDs = slices.Clone(s.RoomAgents.AgentIDs)
	return &out
}

func normalizeAgentIDs(agentIDs []string) []string {
	seen := make(map[string]struct{}, len(agentIDs))
	out := make([]string, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		trimmed := strings.TrimSpace(agentID)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func (s *aiConversationState) ensureDefaults() {
	if s.Kind == "" {
		s.Kind = ConversationKindNormal
	}
	if s.Visibility == "" {
		s.Visibility = ConversationVisibilityNormal
	}
	s.RoomAgents.AgentIDs = normalizeAgentIDs(s.RoomAgents.AgentIDs)
}

const aiConversationStateTable = "ai_conversation_state"

type conversationStateStore struct {
	mu    sync.RWMutex
	rooms map[string]*aiConversationState
}

func newConversationStateStore() *conversationStateStore {
	return &conversationStateStore{rooms: make(map[string]*aiConversationState)}
}

func conversationStateKey(portal *bridgev2.Portal) string {
	if portal == nil || portal.Portal == nil {
		return ""
	}
	if portal.MXID != "" {
		return portal.MXID.String()
	}
	return string(portal.PortalKey.ID) + "\x00" + string(portal.PortalKey.Receiver)
}

func (s *conversationStateStore) get(portal *bridgev2.Portal) *aiConversationState {
	if s == nil || portal == nil {
		return &aiConversationState{}
	}
	key := conversationStateKey(portal)
	if key == "" {
		return &aiConversationState{}
	}
	s.mu.RLock()
	state := s.rooms[key]
	s.mu.RUnlock()
	if state != nil {
		return state.clone()
	}
	return &aiConversationState{}
}

func (s *conversationStateStore) set(portal *bridgev2.Portal, state *aiConversationState) {
	if s == nil || portal == nil || state == nil {
		return
	}
	key := conversationStateKey(portal)
	if key == "" {
		return
	}
	s.mu.Lock()
	s.rooms[key] = state.clone()
	s.mu.Unlock()
}

func conversationStateDB(portal *bridgev2.Portal) (*dbutil.Database, string, string, string) {
	if portal == nil || portal.Portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil || portal.Bridge.DB.Database == nil {
		return nil, "", "", ""
	}
	return portal.Bridge.DB.Database, string(portal.Bridge.DB.BridgeID), string(portal.PortalKey.Receiver), string(portal.PortalKey.ID)
}

func loadConversationState(portal *bridgev2.Portal, store *conversationStateStore) *aiConversationState {
	if portal == nil {
		return &aiConversationState{}
	}
	state := store.get(portal)
	if conversationStateIsEmpty(state) {
		loaded, err := loadConversationStateFromDB(context.Background(), portal)
		if err == nil && loaded != nil {
			state = loaded
		}
	}
	state.ensureDefaults()
	if store != nil {
		store.set(portal, state)
	}
	return state
}

func conversationStateIsEmpty(state *aiConversationState) bool {
	return state == nil ||
		(state.Kind == "" &&
			state.Visibility == "" &&
			len(state.RoomAgents.AgentIDs) == 0 &&
			len(state.Metadata) == 0 &&
			state.ParentConversationID == "" &&
			state.ParentEventID == "" &&
			!state.ArchiveOnCompletion)
}

func loadConversationStateFromDB(ctx context.Context, portal *bridgev2.Portal) (*aiConversationState, error) {
	db, bridgeID, loginID, portalID := conversationStateDB(portal)
	if db == nil {
		return nil, nil
	}
	var raw string
	err := db.QueryRow(ctx, `
		SELECT state_json
		FROM `+aiConversationStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
	`, bridgeID, loginID, portalID).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state aiConversationState
	if err = json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveConversationState(ctx context.Context, portal *bridgev2.Portal, store *conversationStateStore, state *aiConversationState) error {
	if portal == nil || state == nil {
		return nil
	}
	state.ensureDefaults()
	// Always update the in-memory cache, regardless of persistence outcome.
	defer func() {
		if store != nil {
			store.set(portal, state)
		}
	}()
	db, bridgeID, loginID, portalID := conversationStateDB(portal)
	if db == nil {
		return nil
	}
	payload, err := json.Marshal(state.clone())
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
		INSERT INTO `+aiConversationStateTable+` (bridge_id, login_id, portal_id, state_json, updated_at_ms)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, portal_id) DO UPDATE SET
			state_json=excluded.state_json,
			updated_at_ms=excluded.updated_at_ms
	`, bridgeID, loginID, portalID, string(payload), time.Now().UnixMilli())
	return err
}

func DeleteConversationState(ctx context.Context, portal *bridgev2.Portal) error {
	db, bridgeID, loginID, portalID := conversationStateDB(portal)
	if db == nil {
		return nil
	}
	_, err := db.Exec(ctx, `
		DELETE FROM `+aiConversationStateTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND portal_id=$3
	`, bridgeID, loginID, portalID)
	return err
}

func DeleteLoginConversationState(ctx context.Context, db *dbutil.Database, bridgeID, loginID string) error {
	if db == nil || bridgeID == "" || loginID == "" {
		return nil
	}
	_, err := db.Exec(ctx, `
		DELETE FROM `+aiConversationStateTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, bridgeID, loginID)
	return err
}
