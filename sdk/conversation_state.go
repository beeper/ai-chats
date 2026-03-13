package sdk

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"maunium.net/go/mautrix/bridgev2"
)

type sdkConversationState struct {
	Kind                 ConversationKind
	Visibility           ConversationVisibility
	ParentConversationID string
	ParentEventID        string
	ArchiveOnCompletion  bool
	Metadata             map[string]any
	RoomAgents           RoomAgentSet
}

func (s *sdkConversationState) clone() *sdkConversationState {
	if s == nil {
		return &sdkConversationState{}
	}
	out := *s
	if s.Metadata != nil {
		out.Metadata = make(map[string]any, len(s.Metadata))
		for k, v := range s.Metadata {
			out.Metadata[k] = v
		}
	}
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

func (s *sdkConversationState) ensureDefaults() {
	if s.Kind == "" {
		s.Kind = ConversationKindNormal
	}
	if s.Visibility == "" {
		s.Visibility = ConversationVisibilityNormal
	}
	s.RoomAgents.AgentIDs = normalizeAgentIDs(s.RoomAgents.AgentIDs)
	if strings.TrimSpace(s.RoomAgents.PrimaryAgentID) == "" && len(s.RoomAgents.AgentIDs) > 0 {
		s.RoomAgents.PrimaryAgentID = s.RoomAgents.AgentIDs[0]
	}
}

// SDKPortalMetadata can be used as a connector portal metadata type when the SDK owns the portal metadata schema.
type SDKPortalMetadata struct {
	Conversation sdkConversationState `json:"conversation,omitempty"`
}

const sdkConversationMetadataKey = "sdk_conversation"

type conversationStateStore struct {
	mu    sync.RWMutex
	rooms map[string]*sdkConversationState
}

func newConversationStateStore() *conversationStateStore {
	return &conversationStateStore{rooms: make(map[string]*sdkConversationState)}
}

func conversationStateKey(portal *bridgev2.Portal) string {
	if portal == nil {
		return ""
	}
	if portal.MXID != "" {
		return portal.MXID.String()
	}
	return string(portal.PortalKey.ID) + "\x00" + string(portal.PortalKey.Receiver)
}

func (s *conversationStateStore) get(portal *bridgev2.Portal) *sdkConversationState {
	if s == nil || portal == nil {
		return &sdkConversationState{}
	}
	key := conversationStateKey(portal)
	s.mu.RLock()
	state := s.rooms[key]
	s.mu.RUnlock()
	if state != nil {
		return state.clone()
	}
	return &sdkConversationState{}
}

func (s *conversationStateStore) set(portal *bridgev2.Portal, state *sdkConversationState) {
	if s == nil || portal == nil {
		return
	}
	key := conversationStateKey(portal)
	s.mu.Lock()
	s.rooms[key] = state.clone()
	s.mu.Unlock()
}

func loadConversationState(portal *bridgev2.Portal, store *conversationStateStore) *sdkConversationState {
	if portal == nil {
		return &sdkConversationState{}
	}
	if portal.Metadata == nil {
		portal.Metadata = &SDKPortalMetadata{}
	}
	if meta, ok := portal.Metadata.(*SDKPortalMetadata); ok && meta != nil {
		state := meta.Conversation.clone()
		state.ensureDefaults()
		if store != nil {
			store.set(portal, state)
		}
		return state
	}
	if state, ok := loadConversationStateFromGenericMetadata(portal.Metadata); ok {
		state.ensureDefaults()
		if store != nil {
			store.set(portal, state)
		}
		return state
	}
	state := store.get(portal)
	state.ensureDefaults()
	return state
}

func saveConversationState(ctx context.Context, portal *bridgev2.Portal, store *conversationStateStore, state *sdkConversationState) error {
	if portal == nil || state == nil {
		return nil
	}
	state.ensureDefaults()
	if portal.Metadata == nil {
		portal.Metadata = &SDKPortalMetadata{}
	}
	if meta, ok := portal.Metadata.(*SDKPortalMetadata); ok && meta != nil {
		meta.Conversation = *state.clone()
		if err := portal.Save(ctx); err != nil {
			if store != nil {
				store.set(portal, state)
			}
			return err
		}
	} else if saveConversationStateToGenericMetadata(&portal.Metadata, state) {
		if err := portal.Save(ctx); err != nil {
			if store != nil {
				store.set(portal, state)
			}
			return err
		}
	}
	if store != nil {
		store.set(portal, state)
	}
	return nil
}

func loadConversationStateFromGenericMetadata(meta any) (*sdkConversationState, bool) {
	var raw any
	switch typed := meta.(type) {
	case map[string]any:
		raw = typed[sdkConversationMetadataKey]
	case *map[string]any:
		if typed != nil {
			raw = (*typed)[sdkConversationMetadataKey]
		}
	default:
		return nil, false
	}
	if raw == nil {
		return nil, false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var state sdkConversationState
	if err = json.Unmarshal(data, &state); err != nil {
		return nil, false
	}
	return &state, true
}

func saveConversationStateToGenericMetadata(holder *any, state *sdkConversationState) bool {
	if holder == nil || state == nil {
		return false
	}
	switch typed := (*holder).(type) {
	case map[string]any:
		typed[sdkConversationMetadataKey] = state.clone()
		*holder = typed
		return true
	case *map[string]any:
		if typed == nil {
			newMap := map[string]any{sdkConversationMetadataKey: state.clone()}
			*holder = &newMap
			return true
		}
		if *typed == nil {
			*typed = make(map[string]any)
		}
		(*typed)[sdkConversationMetadataKey] = state.clone()
		return true
	default:
		return false
	}
}
