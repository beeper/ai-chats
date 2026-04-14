package ai

import (
	"context"
	"strings"
)

const heartbeatDedupeWindowMs = 24 * 60 * 60 * 1000

func (oc *AIClient) managedHeartbeatStateSnapshot(ctx context.Context, agentID string) *managedHeartbeatState {
	if oc == nil {
		return nil
	}
	scheduler := oc.scheduler
	if scheduler == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	store, err := scheduler.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		oc.log.Warn().Err(err).Str("agent_id", agentID).Msg("managed heartbeat state: load failed")
		return nil
	}
	idx := findManagedHeartbeat(store.Agents, agentID)
	if idx < 0 {
		return nil
	}
	state := store.Agents[idx]
	return &state
}

func (oc *AIClient) updateManagedHeartbeatState(ctx context.Context, agentID string, updater func(*managedHeartbeatState) bool) {
	if oc == nil || updater == nil {
		return
	}
	scheduler := oc.scheduler
	if scheduler == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	store, err := scheduler.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		oc.log.Warn().Err(err).Str("agent_id", agentID).Msg("managed heartbeat state: load failed")
		return
	}
	idx := findManagedHeartbeat(store.Agents, agentID)
	if idx < 0 {
		store.Agents = append(store.Agents, managedHeartbeatState{
			AgentID:  normalizeAgentID(agentID),
			Revision: 1,
		})
		idx = len(store.Agents) - 1
	}
	if !updater(&store.Agents[idx]) {
		return
	}
	if err := scheduler.saveHeartbeatStoreLocked(ctx, store); err != nil {
		oc.log.Warn().Err(err).Str("agent_id", agentID).Msg("managed heartbeat state: save failed")
	}
}

func (oc *AIClient) isDuplicateHeartbeat(agentID string, sessionKey string, text string, nowMs int64) bool {
	if oc == nil {
		return false
	}
	state := oc.managedHeartbeatStateSnapshot(context.Background(), agentID)
	if state == nil {
		return false
	}
	return state.isDuplicateHeartbeat(sessionKey, text, nowMs)
}

func (oc *AIClient) recordHeartbeatText(agentID string, sessionKey string, text string, sentAt int64) {
	if oc == nil {
		return
	}
	oc.updateManagedHeartbeatState(context.Background(), agentID, func(state *managedHeartbeatState) bool {
		return state.recordHeartbeatText(sessionKey, text, sentAt)
	})
}

func (oc *AIClient) restoreHeartbeatUpdatedAt(storeAgentID string, sessionKey string, updatedAt int64) {
	if oc == nil {
		return
	}
	if updatedAt <= 0 {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	currentUpdatedAt, ok := oc.loadSessionUpdatedAt(context.Background(), storeAgentID, sessionKey)
	if !ok {
		return
	}
	if currentUpdatedAt >= updatedAt {
		return
	}
	oc.updateSessionTimestamp(context.Background(), storeAgentID, sessionKey, updatedAt)
}
