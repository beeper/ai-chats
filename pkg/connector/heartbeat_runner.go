package connector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/cron"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

type heartbeatAgentState struct {
	agentID    string
	heartbeat  *HeartbeatConfig
	intervalMs int64
	lastRunMs  int64
	nextDueMs  int64
}

type HeartbeatRunner struct {
	client  *AIClient
	agents  map[string]*heartbeatAgentState
	timer   *time.Timer
	stopped bool
	mu      sync.Mutex
}

func NewHeartbeatRunner(client *AIClient) *HeartbeatRunner {
	return &HeartbeatRunner{
		client: client,
		agents: make(map[string]*heartbeatAgentState),
	}
}

func (r *HeartbeatRunner) Start() {
	if r == nil || r.client == nil {
		return
	}
	r.updateConfig(&r.client.connector.Config)
	if r.client.heartbeatWake != nil {
		r.client.heartbeatWake.SetHandler(func(reason string) cron.HeartbeatRunResult {
			return r.run(reason)
		})
	}
}

func (r *HeartbeatRunner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.mu.Unlock()
	if r.client != nil && r.client.heartbeatWake != nil {
		r.client.heartbeatWake.SetHandler(nil)
	}
}

func (r *HeartbeatRunner) updateConfig(cfg *Config) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	now := time.Now().UnixMilli()
	prev := r.agents
	next := make(map[string]*heartbeatAgentState)
	for _, agent := range resolveHeartbeatAgents(cfg) {
		intervalMs := resolveHeartbeatIntervalMs(cfg, "", agent.heartbeat)
		if intervalMs <= 0 {
			continue
		}
		prevState := prev[agent.agentID]
		nextDue := now + intervalMs
		lastRun := int64(0)
		if prevState != nil {
			lastRun = prevState.lastRunMs
			if prevState.lastRunMs > 0 {
				nextDue = prevState.lastRunMs + intervalMs
			} else if prevState.intervalMs == intervalMs && prevState.nextDueMs > now {
				nextDue = prevState.nextDueMs
			}
		}
		next[agent.agentID] = &heartbeatAgentState{
			agentID:    agent.agentID,
			heartbeat:  agent.heartbeat,
			intervalMs: intervalMs,
			lastRunMs:  lastRun,
			nextDueMs:  nextDue,
		}
	}
	r.agents = next
	r.scheduleNextLocked()
}

func (r *HeartbeatRunner) scheduleNextLocked() {
	if r.stopped {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if len(r.agents) == 0 || r.client == nil || r.client.heartbeatWake == nil {
		return
	}
	now := time.Now().UnixMilli()
	nextDue := int64(0)
	for _, agent := range r.agents {
		if nextDue == 0 || agent.nextDueMs < nextDue {
			nextDue = agent.nextDueMs
		}
	}
	if nextDue == 0 {
		return
	}
	delay := nextDue - now
	if delay < 0 {
		delay = 0
	}
	r.timer = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		r.client.heartbeatWake.Request("interval", 0)
	})
}

func (r *HeartbeatRunner) run(reason string) cron.HeartbeatRunResult {
	r.mu.Lock()
	if r.stopped || len(r.agents) == 0 {
		r.mu.Unlock()
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	agents := make([]*heartbeatAgentState, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	r.mu.Unlock()

	now := time.Now().UnixMilli()
	isInterval := reason == "interval"
	ran := false
	for _, agent := range agents {
		if isInterval && now < agent.nextDueMs {
			continue
		}
		res := r.client.runHeartbeatOnce(agent.agentID, agent.heartbeat, reason)
		if res.Status == "skipped" && res.Reason == "requests-in-flight" {
			return res
		}
		if res.Status != "skipped" || res.Reason != "disabled" {
			r.mu.Lock()
			agent.lastRunMs = now
			agent.nextDueMs = now + agent.intervalMs
			r.mu.Unlock()
		}
		if res.Status == "ran" {
			ran = true
		}
	}
	r.mu.Lock()
	r.scheduleNextLocked()
	r.mu.Unlock()
	if ran {
		return cron.HeartbeatRunResult{Status: "ran"}
	}
	if isInterval {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "not-due"}
	}
	return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
}

type heartbeatAgent struct {
	agentID   string
	heartbeat *HeartbeatConfig
}

func resolveHeartbeatAgents(cfg *Config) []heartbeatAgent {
	list := []heartbeatAgent{}
	if cfg == nil {
		return list
	}
	if hasExplicitHeartbeatAgents(cfg) {
		for _, entry := range cfg.Agents.List {
			if entry.Heartbeat == nil {
				continue
			}
			id := normalizeAgentID(entry.ID)
			if id == "" {
				continue
			}
			list = append(list, heartbeatAgent{agentID: id, heartbeat: resolveHeartbeatConfig(cfg, id)})
		}
		return list
	}
	list = append(list, heartbeatAgent{agentID: normalizeAgentID(agents.DefaultAgentID), heartbeat: resolveHeartbeatConfig(cfg, agents.DefaultAgentID)})
	return list
}

func (oc *AIClient) runHeartbeatOnce(agentID string, heartbeat *HeartbeatConfig, reason string) cron.HeartbeatRunResult {
	if oc == nil || oc.connector == nil {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	cfg := &oc.connector.Config
	if !isHeartbeatEnabledForAgent(cfg, agentID) {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	if resolveHeartbeatIntervalMs(cfg, "", heartbeat) <= 0 {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}

	now := time.Now().UnixMilli()
	if !isWithinActiveHours(oc, heartbeat, now) {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "quiet-hours"}
	}

	if oc.hasInflightRequests() {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "requests-in-flight"}
	}

	sessionResolution := oc.resolveHeartbeatSession(agentID, heartbeat)
	storeKey := strings.TrimSpace(sessionResolution.SessionKey)

	sessionPortal, sessionKey, err := oc.resolveHeartbeatSessionPortal(agentID, heartbeat)
	if err != nil || sessionPortal == nil || sessionPortal.MXID == "" {
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "no-session"}
	}

	// Skip when HEARTBEAT.md exists but is effectively empty.
	pendingEvents := hasSystemEvents(sessionKey) || (storeKey != "" && !strings.EqualFold(storeKey, sessionKey) && hasSystemEvents(storeKey))
	if !oc.shouldRunHeartbeatForFile(agentID, reason) && !pendingEvents {
		emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:     time.Now().UnixMilli(),
			Status: "skipped",
			Reason: "empty-heartbeat-file",
		})
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "empty-heartbeat-file"}
	}

	entry := sessionResolution.Entry
	prevUpdatedAt := int64(0)
	if entry != nil {
		prevUpdatedAt = entry.UpdatedAt
	}

	delivery := oc.resolveHeartbeatDeliveryTarget(agentID, heartbeat, entry)
	deliveryPortal := delivery.Portal
	deliveryRoom := delivery.RoomID
	deliveryReason := delivery.Reason
	channel := delivery.Channel
	visibility := defaultHeartbeatVisibility
	if channel != "" {
		visibility = resolveHeartbeatVisibility(cfg, channel)
	}
	if !visibility.ShowAlerts && !visibility.ShowOk && !visibility.UseIndicator {
		emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:      time.Now().UnixMilli(),
			Status:  "skipped",
			Reason:  "alerts-disabled",
			Channel: channel,
		})
		return cron.HeartbeatRunResult{Status: "skipped", Reason: "alerts-disabled"}
	}
	var agentDef *agents.AgentDefinition
	store := NewAgentStoreAdapter(oc)
	if agent, err := store.GetAgentByID(context.Background(), agentID); err == nil {
		agentDef = agent
	}
	isExecEvent := reason == "exec-event"
	hasExecCompletion := false
	if isExecEvent {
		systemEvents := peekSystemEvents(sessionKey)
		if storeKey != "" && !strings.EqualFold(storeKey, sessionKey) {
			systemEvents = append(systemEvents, peekSystemEvents(storeKey)...)
		}
		for _, evt := range systemEvents {
			if strings.Contains(evt, "Exec finished") {
				hasExecCompletion = true
				break
			}
		}
	}
	suppressSend := deliveryPortal == nil || deliveryRoom == ""
	promptMeta := clonePortalMetadata(portalMeta(sessionPortal))
	if promptMeta == nil {
		promptMeta = &PortalMetadata{}
	}
	if heartbeat != nil && heartbeat.Model != nil {
		if model := strings.TrimSpace(*heartbeat.Model); model != "" {
			promptMeta.Model = model
		}
	}
	responsePrefix := resolveResponsePrefixForHeartbeat(oc, cfg, agentID, promptMeta)
	hbCfg := &HeartbeatRunConfig{
		Reason:           reason,
		AckMaxChars:      resolveHeartbeatAckMaxChars(cfg, heartbeat),
		ShowOk:           visibility.ShowOk,
		ShowAlerts:       visibility.ShowAlerts,
		UseIndicator:     visibility.UseIndicator,
		IncludeReasoning: heartbeat != nil && heartbeat.IncludeReasoning != nil && *heartbeat.IncludeReasoning,
		ExecEvent:        hasExecCompletion,
		ResponsePrefix:   responsePrefix,
		SessionKey:       storeKey,
		StoreAgentID:     sessionResolution.StoreRef.AgentID,
		StorePath:        sessionResolution.StoreRef.Path,
		PrevUpdatedAt:    prevUpdatedAt,
		TargetRoom:       deliveryRoom,
		TargetReason:     deliveryReason,
		SuppressSend:     suppressSend,
		AgentID:          agentID,
		Channel:          channel,
		SuppressSave:     true,
	}
	prompt := resolveHeartbeatPrompt(cfg, heartbeat, agentDef)
	if hasExecCompletion {
		prompt = execEventPrompt
	}
	systemEvents := formatSystemEvents(drainHeartbeatSystemEvents(sessionKey, storeKey))
	if systemEvents != "" {
		prompt = systemEvents + "\n\n" + prompt
	}

	promptMessages, err := oc.buildPromptWithHeartbeat(context.Background(), sessionPortal, promptMeta, prompt)
	if err != nil {
		return cron.HeartbeatRunResult{Status: "failed", Reason: err.Error()}
	}

	resultCh := make(chan HeartbeatRunOutcome, 1)
	runCtx := withHeartbeatRun(oc.backgroundContext(context.Background()), hbCfg, resultCh)
	done := make(chan struct{})
	sendPortal := sessionPortal
	if deliveryPortal != nil && deliveryPortal.MXID != "" {
		sendPortal = deliveryPortal
	}
	go func() {
		oc.streamingResponseWithRetry(runCtx, nil, sendPortal, promptMeta, promptMessages)
		close(done)
	}()

	select {
	case res := <-resultCh:
		return cron.HeartbeatRunResult{Status: res.Status, Reason: res.Reason}
	case <-done:
		return cron.HeartbeatRunResult{Status: "failed", Reason: "heartbeat failed"}
	case <-time.After(2 * time.Minute):
		return cron.HeartbeatRunResult{Status: "failed", Reason: "heartbeat timed out"}
	}
}

func drainHeartbeatSystemEvents(primaryKey string, secondaryKey string) []SystemEvent {
	entries := drainSystemEventEntries(primaryKey)
	if strings.TrimSpace(secondaryKey) != "" && !strings.EqualFold(strings.TrimSpace(primaryKey), strings.TrimSpace(secondaryKey)) {
		entries = append(entries, drainSystemEventEntries(secondaryKey)...)
	}
	if len(entries) <= 1 {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].TS < entries[j].TS
	})
	return entries
}

func (oc *AIClient) buildPromptWithHeartbeat(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, prompt string) ([]openai.ChatCompletionMessageParamUnion, error) {
	base, err := oc.buildBasePrompt(ctx, portal, meta)
	if err != nil {
		return nil, err
	}
	base = oc.injectMemoryContext(ctx, portal, meta, base)
	message := appendMessageIDHint(prompt, "")
	return append(base, openai.UserMessage(message)), nil
}

func (oc *AIClient) resolveHeartbeatSessionPortal(agentID string, heartbeat *HeartbeatConfig) (*bridgev2.Portal, string, error) {
	session := ""
	if heartbeat != nil {
		if heartbeat.Session != nil {
			session = strings.TrimSpace(*heartbeat.Session)
		}
	}
	mainKey := ""
	if oc != nil && oc.connector != nil && oc.connector.Config.Session != nil {
		mainKey = strings.TrimSpace(oc.connector.Config.Session.MainKey)
	}
	if session == "" {
		if portal := oc.defaultChatPortal(); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		if portal := oc.lastActivePortal(agentID); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		return nil, "", errors.New("no session")
	}
	if strings.EqualFold(session, "main") || strings.EqualFold(session, "global") || (mainKey != "" && strings.EqualFold(session, mainKey)) {
		if portal := oc.defaultChatPortal(); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		if portal := oc.lastActivePortal(agentID); portal != nil {
			return portal, portal.MXID.String(), nil
		}
	}
	if strings.HasPrefix(session, "!") {
		if portal, err := oc.UserLogin.Bridge.GetPortalByMXID(context.Background(), id.RoomID(session)); err == nil && portal != nil {
			return portal, portal.MXID.String(), nil
		}
	}
	if portal := oc.defaultChatPortal(); portal != nil {
		return portal, portal.MXID.String(), nil
	}
	if portal := oc.lastActivePortal(agentID); portal != nil {
		return portal, portal.MXID.String(), nil
	}
	return nil, "", errors.New("no session")
}

func (oc *AIClient) shouldRunHeartbeatForFile(agentID string, reason string) bool {
	store := textfs.NewStore(oc.UserLogin.Bridge.DB.Database, string(oc.UserLogin.Bridge.DB.BridgeID), string(oc.UserLogin.ID), normalizeAgentID(agentID))
	entry, found, err := store.Read(context.Background(), agents.DefaultHeartbeatFilename)
	if err != nil || !found {
		return true
	}
	if agents.IsHeartbeatContentEffectivelyEmpty(entry.Content) && reason != "exec-event" {
		return false
	}
	return true
}

func isWithinActiveHours(oc *AIClient, heartbeat *HeartbeatConfig, nowMs int64) bool {
	if heartbeat == nil || heartbeat.ActiveHours == nil {
		return true
	}
	startMin := parseActiveHoursTime(heartbeat.ActiveHours.Start, false)
	endMin := parseActiveHoursTime(heartbeat.ActiveHours.End, true)
	if startMin == nil || endMin == nil {
		return true
	}
	loc := resolveActiveHoursTimezone(oc, heartbeat.ActiveHours.Timezone)
	if loc == nil {
		return true
	}
	now := time.UnixMilli(nowMs).In(loc)
	currentMin := now.Hour()*60 + now.Minute()
	if *endMin > *startMin {
		return currentMin >= *startMin && currentMin < *endMin
	}
	return currentMin >= *startMin || currentMin < *endMin
}

func parseActiveHoursTime(raw string, allow24 bool) *int {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if !activeHoursPattern.MatchString(trimmed) {
		return nil
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return nil
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return nil
	}
	if hour == 24 {
		if !allow24 || minute != 0 {
			return nil
		}
		val := 24 * 60
		return &val
	}
	val := hour*60 + minute
	return &val
}

var activeHoursPattern = regexp.MustCompile(`^([01]\d|2[0-3]|24):([0-5]\d)$`)

const execEventPrompt = "An async command you ran earlier has completed. The result is shown in the system messages above. " +
	"Please relay the command output to the user in a helpful way. If the command succeeded, share the relevant output. " +
	"If it failed, explain what went wrong."

func resolveActiveHoursTimezone(oc *AIClient, raw string) *time.Location {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "user") {
		_, loc := oc.resolveUserTimezone()
		return loc
	}
	if strings.EqualFold(trimmed, "local") {
		return time.Local
	}
	if loc, err := time.LoadLocation(trimmed); err == nil {
		return loc
	}
	_, loc := oc.resolveUserTimezone()
	return loc
}

func formatSystemEvents(events []SystemEvent) string {
	if len(events) == 0 {
		return ""
	}
	lines := make([]string, 0, len(events))
	for _, evt := range events {
		text := compactSystemEvent(evt.Text)
		if text == "" {
			continue
		}
		ts := formatSystemEventTimestamp(evt.TS)
		lines = append(lines, fmt.Sprintf("System: [%s] %s", ts, text))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

var nodeLastInputRe = regexp.MustCompile(`(?i)\s*·\s*last input [^·]+`)

func compactSystemEvent(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	lowered := strings.ToLower(trimmed)
	if strings.Contains(lowered, "reason periodic") {
		return ""
	}
	// Filter out the actual heartbeat prompt, but not cron jobs that mention "heartbeat".
	if strings.HasPrefix(lowered, "read heartbeat.md") {
		return ""
	}
	// Filter heartbeat poll/wake noise.
	if strings.Contains(lowered, "heartbeat poll") || strings.Contains(lowered, "heartbeat wake") {
		return ""
	}
	if strings.HasPrefix(trimmed, "Node:") {
		trimmed = strings.TrimSpace(nodeLastInputRe.ReplaceAllString(trimmed, ""))
	}
	return trimmed
}

func formatSystemEventTimestamp(ts int64) string {
	if ts <= 0 {
		return "unknown-time"
	}
	date := time.UnixMilli(ts).In(time.Local)
	if date.IsZero() {
		return "unknown-time"
	}
	return date.Format("2006-01-02 15:04:05 MST")
}
