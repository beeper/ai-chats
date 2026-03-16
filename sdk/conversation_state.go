package sdk

import (
	"context"
	"encoding/json"
	"maps"
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

func (s *sdkConversationState) ensureDefaults() {
	if s.Kind == "" {
		s.Kind = ConversationKindNormal
	}
	if s.Visibility == "" {
		s.Visibility = ConversationVisibilityNormal
	}
	s.RoomAgents.AgentIDs = normalizeAgentIDs(s.RoomAgents.AgentIDs)
}

// SDKPortalMetadata can be used as a connector portal metadata type when the SDK owns the portal metadata schema.
type SDKPortalMetadata struct {
	Conversation sdkConversationState `json:"conversation,omitempty"`
}

// ConversationStateCarrier allows bridge-specific portal metadata types to
// preserve SDK conversation state alongside their own fields.
type ConversationStateCarrier interface {
	GetSDKPortalMetadata() *SDKPortalMetadata
	SetSDKPortalMetadata(*SDKPortalMetadata)
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
	state := loadConversationStateFromMetadata(portal.Metadata)
	if state == nil {
		state = store.get(portal)
	}
	state.ensureDefaults()
	if store != nil {
		store.set(portal, state)
	}
	return state
}

func loadConversationStateFromMetadata(metadata any) *sdkConversationState {
	if meta, ok := metadata.(*SDKPortalMetadata); ok && meta != nil {
		return meta.Conversation.clone()
	}
	if carrier, ok := metadata.(ConversationStateCarrier); ok && carrier != nil {
		if meta := carrier.GetSDKPortalMetadata(); meta != nil {
			return meta.Conversation.clone()
		}
	}
	if state, ok := loadConversationStateFromGenericMetadata(metadata); ok {
		return state
	}
	return nil
}

func saveConversationState(ctx context.Context, portal *bridgev2.Portal, store *conversationStateStore, state *sdkConversationState) error {
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
	if portal.Metadata == nil {
		portal.Metadata = &SDKPortalMetadata{}
	}
	needsSave := false
	switch meta := portal.Metadata.(type) {
	case *SDKPortalMetadata:
		if meta != nil {
			meta.Conversation = *state.clone()
			needsSave = true
		}
	case ConversationStateCarrier:
		if meta != nil {
			sdkMeta := meta.GetSDKPortalMetadata()
			if sdkMeta == nil {
				sdkMeta = &SDKPortalMetadata{}
			}
			sdkMeta.Conversation = *state.clone()
			meta.SetSDKPortalMetadata(sdkMeta)
			needsSave = true
		}
	default:
		needsSave = saveConversationStateToGenericMetadata(&portal.Metadata, state)
	}
	if needsSave {
		return portal.Save(ctx)
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
