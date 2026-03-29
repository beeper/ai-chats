package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

func (s *schedulerRuntime) RunHeartbeatSweep(ctx context.Context, reason string) (string, string) {
	if s == nil || s.client == nil || !s.client.agentsEnabledForLogin() {
		return "skipped", "disabled"
	}
	agents, err := s.schedulableHeartbeatAgents(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to resolve schedulable heartbeat agents")
		return "skipped", "disabled"
	}
	if len(agents) == 0 {
		return "skipped", "disabled"
	}
	ran := false
	blocked := false
	for _, agent := range agents {
		res := s.client.runHeartbeatOnce(agent.agentID, agent.heartbeat, reason)
		if res.Status == "skipped" && res.Reason == "requests-in-flight" {
			blocked = true
			continue
		}
		if res.Status == "ran" {
			ran = true
		}
	}
	if ran {
		return "ran", ""
	}
	if blocked {
		return "skipped", "requests-in-flight"
	}
	return "skipped", "disabled"
}

func (s *schedulerRuntime) RequestHeartbeatNow(ctx context.Context, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agents, err := s.schedulableHeartbeatAgents(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to resolve schedulable heartbeat agents for immediate wake")
		return
	}
	agents = s.wakeableHeartbeatAgents(agents)
	if len(agents) == 0 {
		return
	}
	store, err := s.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		s.client.log.Warn().Err(err).Msg("Failed to load managed heartbeat store")
		return
	}
	nowMs := time.Now().UnixMilli()
	changed := false
	for _, agent := range agents {
		state := upsertManagedHeartbeat(&store, agent.agentID, agent.heartbeat)
		if state == nil || !state.Enabled {
			continue
		}
		if state.NextRunAtMs > 0 && state.NextRunAtMs-nowMs <= int64(scheduleHeartbeatCoalesce/time.Millisecond) {
			continue
		}
		if err := s.ensureHeartbeatRoomLocked(ctx, state); err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", agent.agentID).Msg("Failed to ensure heartbeat room for immediate wake")
			continue
		}
		if state.PendingDelayID != "" {
			if err := s.cancelPendingDelayLocked(ctx, state.PendingDelayID); err != nil {
				s.client.log.Warn().Err(err).Str("agent_id", agent.agentID).Msg("Failed to cancel pending heartbeat delay before wake")
				continue
			}
		}
		runAtMs := nowMs + int64(scheduleImmediateDelay/time.Millisecond)
		runKey := buildTickRunKey(state.Revision, "wake", runAtMs)
		resp, err := s.scheduleTickLocked(ctx, id.RoomID(state.RoomID), ScheduleTickContent{
			Kind:           scheduleTickKindHeartbeatRun,
			EntityID:       state.AgentID,
			Revision:       state.Revision,
			ScheduledForMs: runAtMs,
			RunKey:         runKey,
			Reason:         strings.TrimSpace(reason),
		}, scheduleImmediateDelay)
		if err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", agent.agentID).Msg("Failed to schedule immediate heartbeat tick")
			continue
		}
		state.NextRunAtMs = runAtMs
		state.PendingDelayID = string(resp.UnstableDelayID)
		state.PendingDelayKind = "wake"
		state.PendingRunKey = runKey
		changed = true
	}
	if changed {
		if err := s.saveHeartbeatStoreLocked(ctx, store); err != nil {
			s.client.log.Warn().Err(err).Msg("Failed to save managed heartbeat store after wake")
		}
	}
}

func (s *schedulerRuntime) reconcileHeartbeatLocked(ctx context.Context) error {
	store, err := s.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		return err
	}
	agents, err := s.schedulableHeartbeatAgentsWithUserChats(ctx)
	if err != nil {
		return err
	}
	nowMs := time.Now().UnixMilli()
	active := make(map[string]struct{})
	for _, agent := range agents {
		active[agent.agentID] = struct{}{}
		state := upsertManagedHeartbeat(&store, agent.agentID, agent.heartbeat)
		if state == nil || !state.Enabled {
			continue
		}
		if err := s.ensureHeartbeatRoomLocked(ctx, state); err != nil {
			return err
		}
		s.scheduleHeartbeatStateLocked(ctx, state, nowMs, true)
	}
	retained := make([]managedHeartbeatState, 0, len(store.Agents))
	for i := range store.Agents {
		state := &store.Agents[i]
		if _, ok := active[state.AgentID]; ok {
			retained = append(retained, *state)
			continue
		}
		if err := s.cancelPendingDelayLocked(ctx, state.PendingDelayID); err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to cancel disabled heartbeat delay")
		}
	}
	store.Agents = retained
	return s.saveHeartbeatStoreLocked(ctx, store)
}

func (s *schedulerRuntime) handleHeartbeatPlan(ctx context.Context, tick ScheduleTickContent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	store, err := s.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		return err
	}
	idx := findManagedHeartbeat(store.Agents, tick.EntityID)
	if idx < 0 {
		return nil
	}
	state := &store.Agents[idx]
	if !state.Enabled || state.Revision != tick.Revision || containsRunKey(state.ProcessedRunKeys, tick.RunKey) {
		return nil
	}
	state.PendingDelayID = ""
	state.PendingDelayKind = ""
	state.PendingRunKey = ""
	state.ProcessedRunKeys = appendRunKey(state.ProcessedRunKeys, tick.RunKey)
	s.scheduleHeartbeatStateLocked(ctx, state, time.Now().UnixMilli(), false)
	return s.saveHeartbeatStoreLocked(ctx, store)
}

func (s *schedulerRuntime) handleHeartbeatRun(ctx context.Context, tick ScheduleTickContent) error {
	s.mu.Lock()
	store, err := s.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	idx := findManagedHeartbeat(store.Agents, tick.EntityID)
	if idx < 0 {
		s.mu.Unlock()
		return nil
	}
	state := store.Agents[idx]
	if !state.Enabled || state.Revision != tick.Revision || containsRunKey(state.ProcessedRunKeys, tick.RunKey) {
		s.mu.Unlock()
		return nil
	}
	state.PendingDelayID = ""
	state.PendingDelayKind = ""
	state.PendingRunKey = ""
	store.Agents[idx] = state
	if err := s.saveHeartbeatStoreLocked(ctx, store); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	reason := strings.TrimSpace(tick.Reason)
	if reason == "" {
		reason = "interval"
	}
	hb := resolveHeartbeatConfig(&s.client.connector.Config, state.AgentID)
	res := s.client.runHeartbeatOnce(state.AgentID, hb, reason)

	s.mu.Lock()
	defer s.mu.Unlock()
	store, err = s.loadHeartbeatStoreLocked(ctx)
	if err != nil {
		return err
	}
	idx = findManagedHeartbeat(store.Agents, tick.EntityID)
	if idx < 0 {
		return nil
	}
	state = store.Agents[idx]
	if !state.Enabled || state.Revision != tick.Revision || containsRunKey(state.ProcessedRunKeys, tick.RunKey) {
		return nil
	}
	state.LastResult = res.Status
	state.LastError = res.Reason
	finishedAtMs := time.Now().UnixMilli()
	if res.Status == "ran" || res.Status == "sent" {
		state.LastRunAtMs = finishedAtMs
		s.scheduleNextHeartbeatAfterRunLocked(ctx, &state, finishedAtMs)
	} else {
		s.scheduleHeartbeatRetryLocked(ctx, &state, finishedAtMs)
	}
	state.ProcessedRunKeys = appendRunKey(state.ProcessedRunKeys, tick.RunKey)
	store.Agents[idx] = state
	return s.saveHeartbeatStoreLocked(ctx, store)
}

func (s *schedulerRuntime) scheduleHeartbeatStateLocked(ctx context.Context, state *managedHeartbeatState, nowMs int64, validateExisting bool) {
	if state == nil || !state.Enabled || state.IntervalMs <= 0 {
		if state != nil {
			state.NextRunAtMs = 0
			state.PendingDelayID = ""
			state.PendingDelayKind = ""
			state.PendingRunKey = ""
		}
		return
	}
	nextRun := computeManagedHeartbeatDue(s.client, *state, nowMs)
	if nextRun <= 0 {
		return
	}
	if validateExisting && state.PendingDelayID != "" {
		exists, err := s.delayedEventExistsLocked(ctx, state.PendingDelayID)
		if err != nil {
			s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to validate existing heartbeat delay")
			state.LastResult = "error"
			state.LastError = err.Error()
			return
		}
		if exists {
			state.NextRunAtMs = nextRun
			return
		}
	}
	if state.PendingDelayID != "" {
		_ = s.cancelPendingDelayLocked(ctx, state.PendingDelayID)
	}
	kind := scheduleTickKindHeartbeatRun
	runAtMs := nextRun
	if nextRun-nowMs > int64(schedulePlannerHorizon/time.Millisecond) {
		kind = scheduleTickKindHeartbeatPlan
		runAtMs = nowMs + int64(schedulePlannerHorizon/time.Millisecond)
	}
	resp, err := s.scheduleTickLocked(ctx, id.RoomID(state.RoomID), ScheduleTickContent{
		Kind:           kind,
		EntityID:       state.AgentID,
		Revision:       state.Revision,
		ScheduledForMs: runAtMs,
		RunKey:         buildTickRunKey(state.Revision, shortTickKind(kind), runAtMs),
		Reason:         "interval",
	}, time.Duration(max64(runAtMs-nowMs, scheduleImmediateDelay.Milliseconds()))*time.Millisecond)
	if err != nil {
		s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to schedule managed heartbeat tick")
		state.LastResult = "error"
		state.LastError = err.Error()
		return
	}
	state.NextRunAtMs = nextRun
	state.PendingDelayID = string(resp.UnstableDelayID)
	state.PendingDelayKind = shortTickKind(kind)
	state.PendingRunKey = buildTickRunKey(state.Revision, shortTickKind(kind), runAtMs)
}

func (s *schedulerRuntime) scheduleNextHeartbeatAfterRunLocked(ctx context.Context, state *managedHeartbeatState, nowMs int64) {
	if state == nil {
		return
	}
	state.NextRunAtMs = nowMs + state.IntervalMs
	s.scheduleHeartbeatStateLocked(ctx, state, nowMs, false)
}

func (s *schedulerRuntime) scheduleHeartbeatRetryLocked(ctx context.Context, state *managedHeartbeatState, nowMs int64) {
	if state == nil || !state.Enabled {
		return
	}
	if state.PendingDelayID != "" {
		_ = s.cancelPendingDelayLocked(ctx, state.PendingDelayID)
	}
	retryAtMs := nowMs + int64(scheduleHeartbeatCoalesce/time.Millisecond)
	resp, err := s.scheduleTickLocked(ctx, id.RoomID(state.RoomID), ScheduleTickContent{
		Kind:           scheduleTickKindHeartbeatRun,
		EntityID:       state.AgentID,
		Revision:       state.Revision,
		ScheduledForMs: retryAtMs,
		RunKey:         buildTickRunKey(state.Revision, "retry", retryAtMs),
		Reason:         "retry",
	}, scheduleHeartbeatCoalesce)
	if err != nil {
		s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to schedule heartbeat retry tick")
		state.LastResult = "error"
		state.LastError = err.Error()
		return
	}
	state.NextRunAtMs = retryAtMs
	state.PendingDelayID = string(resp.UnstableDelayID)
	state.PendingDelayKind = "retry"
	state.PendingRunKey = buildTickRunKey(state.Revision, "retry", retryAtMs)
}

func computeManagedHeartbeatDue(client *AIClient, state managedHeartbeatState, nowMs int64) int64 {
	if state.IntervalMs <= 0 {
		return 0
	}
	var dueAtMs int64
	if state.LastRunAtMs > 0 {
		dueAtMs = state.LastRunAtMs + state.IntervalMs
		return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
	}
	if client != nil {
		ref, sessionKey := client.resolveHeartbeatMainSessionRef(state.AgentID)
		if entry, ok := client.getSessionEntry(context.Background(), ref, sessionKey); ok && entry.LastHeartbeatSentAt > 0 {
			dueAtMs = entry.LastHeartbeatSentAt + state.IntervalMs
			return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
		}
	}
	dueAtMs = nowMs + state.IntervalMs
	return clampHeartbeatDueToActiveHours(client, state.ActiveHours, dueAtMs)
}

func upsertManagedHeartbeat(store *managedHeartbeatStore, agentID string, hb *HeartbeatConfig) *managedHeartbeatState {
	if store == nil {
		return nil
	}
	idx := findManagedHeartbeat(store.Agents, agentID)
	interval := resolveHeartbeatIntervalMs(nil, "", hb)
	if idx < 0 {
		state := managedHeartbeatState{
			AgentID:     normalizeAgentID(agentID),
			Enabled:     interval > 0,
			IntervalMs:  interval,
			ActiveHours: cloneHeartbeatActiveHours(hb),
			Revision:    1,
		}
		store.Agents = append(store.Agents, state)
		return &store.Agents[len(store.Agents)-1]
	}
	state := &store.Agents[idx]
	if state.Revision <= 0 {
		state.Revision = 1
	}
	if state.IntervalMs != interval || !equalHeartbeatActiveHours(state.ActiveHours, cloneHeartbeatActiveHours(hb)) {
		state.IntervalMs = interval
		state.ActiveHours = cloneHeartbeatActiveHours(hb)
		state.Revision++
		state.PendingDelayID = ""
		state.PendingDelayKind = ""
		state.PendingRunKey = ""
	}
	state.Enabled = interval > 0
	return state
}

func cloneHeartbeatActiveHours(hb *HeartbeatConfig) *HeartbeatActiveHoursConfig {
	if hb == nil || hb.ActiveHours == nil {
		return nil
	}
	copyCfg := *hb.ActiveHours
	return &copyCfg
}

func equalHeartbeatActiveHours(a, b *HeartbeatActiveHoursConfig) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Start == b.Start && a.End == b.End && a.Timezone == b.Timezone
}

func findManagedHeartbeat(states []managedHeartbeatState, agentID string) int {
	trimmed := normalizeAgentID(agentID)
	for idx := range states {
		if normalizeAgentID(states[idx].AgentID) == trimmed {
			return idx
		}
	}
	return -1
}

// schedulableHeartbeatAgents returns heartbeat agents that are configured
// and exist in the agent store.
func (s *schedulerRuntime) schedulableHeartbeatAgents(ctx context.Context) ([]heartbeatAgent, error) {
	if s == nil || s.client == nil || s.client.connector == nil {
		return nil, nil
	}
	candidates := resolveHeartbeatAgents(&s.client.connector.Config)
	if len(candidates) == 0 || !s.client.agentsEnabledForLogin() {
		return nil, nil
	}
	agentsMap, err := NewAgentStoreAdapter(s.client).LoadAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]heartbeatAgent, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := agentsMap[c.agentID]; !ok {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// schedulableHeartbeatAgentsWithUserChats applies the user-chat portal filter
// used by reconcile without forcing sweep and wake paths to enumerate portals.
func (s *schedulerRuntime) schedulableHeartbeatAgentsWithUserChats(ctx context.Context) ([]heartbeatAgent, error) {
	candidates, err := s.schedulableHeartbeatAgents(ctx)
	if err != nil || len(candidates) == 0 {
		return candidates, err
	}
	portals, err := s.client.listAllChatPortals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]heartbeatAgent, 0, len(candidates))
	for _, c := range candidates {
		if !agentHasUserChat(portals, c.agentID) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// wakeableHeartbeatAgents keeps only agents that currently resolve to a
// concrete heartbeat session portal, avoiding managed wake scheduling for
// agents with no active delivery target.
func (s *schedulerRuntime) wakeableHeartbeatAgents(candidates []heartbeatAgent) []heartbeatAgent {
	if s == nil || s.client == nil || len(candidates) == 0 {
		return nil
	}
	out := make([]heartbeatAgent, 0, len(candidates))
	for _, candidate := range candidates {
		portal, _, err := s.client.resolveHeartbeatSessionPortal(candidate.agentID, candidate.heartbeat)
		if err != nil || portal == nil || portal.MXID == "" {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

// agentHasUserChat returns true if the agent has at least one user-facing
// (non-internal, non-subagent) chat portal.
func agentHasUserChat(portals []*bridgev2.Portal, agentID string) bool {
	target := normalizeAgentID(agentID)
	for _, p := range portals {
		if p == nil {
			continue
		}
		meta := portalMeta(p)
		if isModuleInternalRoom(meta) || (meta != nil && meta.SubagentParentRoomID != "") {
			continue
		}
		if normalizeAgentID(resolveAgentID(meta)) == target {
			return true
		}
	}
	return false
}
