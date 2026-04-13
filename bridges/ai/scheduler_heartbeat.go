package ai

import (
	"context"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
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
		s.cancelScheduledTickLocked(heartbeatTimerKey(state.AgentID))
		runAtMs := nowMs + int64(scheduleImmediateDelay/time.Millisecond)
		runKey := buildTickRunKey(state.Revision, "wake", runAtMs)
		err := s.scheduleTickLocked(ctx, heartbeatTimerKey(state.AgentID), ScheduleTickContent{
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
		s.cancelScheduledTickLocked(heartbeatTimerKey(state.AgentID))
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
	if !state.acceptsTick(tick) {
		return nil
	}
	state.markRunProcessed(tick.RunKey)
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
	if !state.acceptsTick(tick) {
		s.mu.Unlock()
		return nil
	}
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
	if !state.acceptsTick(tick) {
		return nil
	}
	finishedAtMs := time.Now().UnixMilli()
	if state.recordRunResult(res, finishedAtMs) {
		s.scheduleNextHeartbeatAfterRunLocked(ctx, &state, finishedAtMs)
	} else {
		s.scheduleHeartbeatRetryLocked(ctx, &state, finishedAtMs)
	}
	state.markRunProcessed(tick.RunKey)
	store.Agents[idx] = state
	return s.saveHeartbeatStoreLocked(ctx, store)
}

func (s *schedulerRuntime) scheduleHeartbeatStateLocked(ctx context.Context, state *managedHeartbeatState, nowMs int64, validateExisting bool) {
	if state == nil || !state.Enabled || state.IntervalMs <= 0 {
		if state != nil {
			state.NextRunAtMs = 0
			state.PendingRunKey = ""
		}
		return
	}
	nextRun := state.dueAt(s.client, nowMs)
	if nextRun <= 0 {
		return
	}
	if validateExisting && state.PendingRunKey != "" && s.hasScheduledTickLocked(heartbeatTimerKey(state.AgentID)) {
		state.NextRunAtMs = nextRun
		return
	}
	s.cancelScheduledTickLocked(heartbeatTimerKey(state.AgentID))
	kind := scheduleTickKindHeartbeatRun
	runAtMs := nextRun
	if nextRun-nowMs > int64(schedulePlannerHorizon/time.Millisecond) {
		kind = scheduleTickKindHeartbeatPlan
		runAtMs = nowMs + int64(schedulePlannerHorizon/time.Millisecond)
	}
	runKey := buildTickRunKey(state.Revision, shortTickKind(kind), runAtMs)
	err := s.scheduleTickLocked(ctx, heartbeatTimerKey(state.AgentID), ScheduleTickContent{
		Kind:           kind,
		EntityID:       state.AgentID,
		Revision:       state.Revision,
		ScheduledForMs: runAtMs,
		RunKey:         runKey,
		Reason:         "interval",
	}, time.Duration(max64(runAtMs-nowMs, scheduleImmediateDelay.Milliseconds()))*time.Millisecond)
	if err != nil {
		s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to schedule managed heartbeat tick")
		state.recordScheduleError(err)
		return
	}
	state.markRunScheduled(nextRun, runKey)
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
	s.cancelScheduledTickLocked(heartbeatTimerKey(state.AgentID))
	retryAtMs := nowMs + int64(scheduleHeartbeatCoalesce/time.Millisecond)
	runKey := buildTickRunKey(state.Revision, "retry", retryAtMs)
	err := s.scheduleTickLocked(ctx, heartbeatTimerKey(state.AgentID), ScheduleTickContent{
		Kind:           scheduleTickKindHeartbeatRun,
		EntityID:       state.AgentID,
		Revision:       state.Revision,
		ScheduledForMs: retryAtMs,
		RunKey:         runKey,
		Reason:         "retry",
	}, scheduleHeartbeatCoalesce)
	if err != nil {
		s.client.log.Warn().Err(err).Str("agent_id", state.AgentID).Msg("Failed to schedule heartbeat retry tick")
		state.recordScheduleError(err)
		return
	}
	state.markRunScheduled(retryAtMs, runKey)
}

func upsertManagedHeartbeat(store *managedHeartbeatStore, agentID string, hb *HeartbeatConfig) *managedHeartbeatState {
	if store == nil {
		return nil
	}
	idx := findManagedHeartbeat(store.Agents, agentID)
	if idx < 0 {
		state := managedHeartbeatState{
			AgentID:  normalizeAgentID(agentID),
			Revision: 1,
		}
		state.applyConfig(agentID, hb)
		store.Agents = append(store.Agents, state)
		return &store.Agents[len(store.Agents)-1]
	}
	state := &store.Agents[idx]
	state.applyConfig(agentID, hb)
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
		if (meta != nil && meta.InternalRoom()) || (meta != nil && meta.SubagentParentRoomID != "") {
			continue
		}
		if normalizeAgentID(resolveAgentID(meta)) == target {
			return true
		}
	}
	return false
}
