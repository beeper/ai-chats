package ai

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/textfs"
)

type heartbeatRunResult struct {
	Status string
	Reason string
}

type heartbeatAgent struct {
	agentID   string
	heartbeat *HeartbeatConfig
}

func resolveHeartbeatAgents(cfg *Config) []heartbeatAgent {
	var list []heartbeatAgent
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

func (oc *AIClient) runHeartbeatOnce(agentID string, heartbeat *HeartbeatConfig, reason string) heartbeatRunResult {
	if oc == nil || oc.connector == nil {
		return heartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	startedAtMs := time.Now().UnixMilli()
	cfg := &oc.connector.Config
	if !isHeartbeatEnabledForAgent(cfg, agentID) {
		oc.log.Debug().Str("agent_id", agentID).Msg("Heartbeat skipped: not enabled for agent")
		return heartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}
	if resolveHeartbeatIntervalMs(cfg, "", heartbeat) <= 0 {
		oc.log.Debug().Str("agent_id", agentID).Msg("Heartbeat skipped: interval <= 0")
		return heartbeatRunResult{Status: "skipped", Reason: "disabled"}
	}

	now := time.Now().UnixMilli()
	if !isWithinActiveHours(oc, heartbeat, now) {
		oc.log.Debug().Str("agent_id", agentID).Msg("Heartbeat skipped: outside active hours")
		return heartbeatRunResult{Status: "skipped", Reason: "quiet-hours"}
	}

	if oc.hasInflightRequests() {
		oc.log.Debug().Str("agent_id", agentID).Msg("Heartbeat skipped: requests in flight")
		return heartbeatRunResult{Status: "skipped", Reason: "requests-in-flight"}
	}

	sessionResolution := oc.resolveHeartbeatSession(agentID, heartbeat)
	storeKey := strings.TrimSpace(sessionResolution.SessionKey)

	sessionPortal, sessionKey, err := oc.resolveHeartbeatSessionPortal(agentID, heartbeat)
	if err != nil || sessionPortal == nil || sessionPortal.MXID == "" {
		oc.log.Warn().Str("agent_id", agentID).Err(err).Msg("Heartbeat skipped: no session portal")
		return heartbeatRunResult{Status: "skipped", Reason: "no-session"}
	}

	ownerKey := systemEventsOwnerKey(oc)
	pendingEvents := hasSystemEvents(ownerKey, sessionKey) || (storeKey != "" && !strings.EqualFold(storeKey, sessionKey) && hasSystemEvents(ownerKey, storeKey))
	if !oc.shouldRunHeartbeatForFile(agentID, reason) && !pendingEvents {
		oc.log.Debug().Str("agent_id", agentID).Msg("Heartbeat skipped: empty heartbeat file and no pending events")
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:         time.Now().UnixMilli(),
			Status:     "skipped",
			Reason:     "empty-heartbeat-file",
			DurationMs: time.Now().UnixMilli() - startedAtMs,
		})
		return heartbeatRunResult{Status: "skipped", Reason: "empty-heartbeat-file"}
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
		oc.log.Debug().Str("agent_id", agentID).Str("channel", channel).Msg("Heartbeat skipped: all visibility flags disabled")
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:         time.Now().UnixMilli(),
			Status:     "skipped",
			Reason:     "alerts-disabled",
			Channel:    channel,
			DurationMs: time.Now().UnixMilli() - startedAtMs,
		})
		return heartbeatRunResult{Status: "skipped", Reason: "alerts-disabled"}
	}
	var agentDef *agents.AgentDefinition
	store := NewAgentStoreAdapter(oc)
	if agent, err := store.GetAgentByID(context.Background(), agentID); err == nil {
		agentDef = agent
	}
	isExecEvent := reason == "exec-event"
	hasExecCompletion := false
	if isExecEvent {
		systemEvents := peekSystemEvents(ownerKey, sessionKey)
		if storeKey != "" && !strings.EqualFold(storeKey, sessionKey) {
			systemEvents = append(systemEvents, peekSystemEvents(ownerKey, storeKey)...)
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
	hbCfg := &HeartbeatRunConfig{
		Reason:           reason,
		AckMaxChars:      resolveHeartbeatAckMaxChars(cfg, heartbeat),
		ShowOk:           visibility.ShowOk,
		ShowAlerts:       visibility.ShowAlerts,
		UseIndicator:     visibility.UseIndicator,
		IncludeReasoning: heartbeat != nil && heartbeat.IncludeReasoning != nil && *heartbeat.IncludeReasoning,
		ExecEvent:        hasExecCompletion,
		SessionKey:       storeKey,
		StoreAgentID:     sessionResolution.StoreRef.AgentID,
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
	systemEvents := ""
	if !suppressSend {
		systemEvents = formatSystemEvents(drainHeartbeatSystemEvents(ownerKey, sessionKey, storeKey))
		if systemEvents != "" {
			prompt = systemEvents + "\n\n" + prompt
			persistSystemEventsSnapshot(oc)
		}
	}

	promptContext, err := oc.buildContextWithHeartbeat(context.Background(), sessionPortal, promptMeta, prompt)
	if err != nil {
		oc.log.Warn().Str("agent_id", agentID).Str("reason", reason).Err(err).Msg("Heartbeat failed to build prompt")
		indicator := (*HeartbeatIndicatorType)(nil)
		if hbCfg.UseIndicator {
			indicator = resolveIndicatorType("failed")
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        "failed",
			Reason:        err.Error(),
			Channel:       hbCfg.Channel,
			To:            hbCfg.TargetRoom.String(),
			DurationMs:    time.Now().UnixMilli() - startedAtMs,
			IndicatorType: indicator,
		})
		return heartbeatRunResult{Status: "failed", Reason: err.Error()}
	}

	oc.log.Info().
		Str("agent_id", agentID).
		Str("reason", reason).
		Str("session_key", sessionKey).
		Str("channel", channel).
		Bool("suppress_send", suppressSend).
		Bool("has_system_events", systemEvents != "").
		Int("prompt_messages", len(promptContext.Messages)).
		Msg("Heartbeat executing")

	resultCh := make(chan HeartbeatRunOutcome, 1)
	timeoutCtx, cancel := context.WithTimeout(oc.backgroundContext(context.Background()), 2*time.Minute)
	defer cancel()
	runCtx := withHeartbeatRun(timeoutCtx, hbCfg, resultCh)
	done := make(chan struct{})
	sendPortal := sessionPortal
	if deliveryPortal != nil && deliveryPortal.MXID != "" {
		sendPortal = deliveryPortal
	}
	go func() {
		oc.runAgentLoopWithRetry(runCtx, nil, sendPortal, promptMeta, promptContext)
		close(done)
	}()

	select {
	case res := <-resultCh:
		oc.log.Info().Str("agent_id", agentID).Str("status", res.Status).Str("result_reason", res.Reason).Msg("Heartbeat completed")
		return heartbeatRunResult{Status: res.Status, Reason: res.Reason}
	case <-done:
		oc.log.Warn().Str("agent_id", agentID).Msg("Heartbeat failed: stream completed without outcome")
		indicator := (*HeartbeatIndicatorType)(nil)
		if hbCfg.UseIndicator {
			indicator = resolveIndicatorType("failed")
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        "failed",
			Reason:        "stream-finished-without-outcome",
			Channel:       hbCfg.Channel,
			To:            hbCfg.TargetRoom.String(),
			DurationMs:    time.Now().UnixMilli() - startedAtMs,
			IndicatorType: indicator,
		})
		return heartbeatRunResult{Status: "failed", Reason: "heartbeat failed"}
	case <-timeoutCtx.Done():
		oc.log.Warn().Str("agent_id", agentID).Msg("Heartbeat timed out after 2 minutes")
		indicator := (*HeartbeatIndicatorType)(nil)
		if hbCfg.UseIndicator {
			indicator = resolveIndicatorType("failed")
		}
		oc.emitHeartbeatEvent(&HeartbeatEventPayload{
			TS:            time.Now().UnixMilli(),
			Status:        "failed",
			Reason:        "timeout",
			Channel:       hbCfg.Channel,
			To:            hbCfg.TargetRoom.String(),
			DurationMs:    time.Now().UnixMilli() - startedAtMs,
			IndicatorType: indicator,
		})
		return heartbeatRunResult{Status: "failed", Reason: "heartbeat timed out"}
	}
}

func drainHeartbeatSystemEvents(ownerKey string, primaryKey string, secondaryKey string) []SystemEvent {
	entries := drainSystemEventEntries(ownerKey, primaryKey)
	if sk := strings.TrimSpace(secondaryKey); sk != "" && !strings.EqualFold(strings.TrimSpace(primaryKey), sk) {
		entries = append(entries, drainSystemEventEntries(ownerKey, secondaryKey)...)
	}
	if len(entries) <= 1 {
		return entries
	}
	slices.SortStableFunc(entries, func(a, b SystemEvent) int {
		return cmp.Compare(a.TS, b.TS)
	})
	return entries
}

func systemEventsOwnerKey(oc *AIClient) string {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return ""
	}
	return string(oc.UserLogin.Bridge.DB.BridgeID) + "|" + string(oc.UserLogin.ID)
}

func (oc *AIClient) buildContextWithHeartbeat(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, prompt string) (PromptContext, error) {
	base, err := oc.buildBaseContext(ctx, portal, meta)
	if err != nil {
		return PromptContext{}, err
	}
	base.Messages = append(base.Messages, PromptMessage{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: prompt,
		}},
	})
	return base, nil
}

func (oc *AIClient) resolveHeartbeatSessionPortal(agentID string, heartbeat *HeartbeatConfig) (*bridgev2.Portal, string, error) {
	session := ""
	if heartbeat != nil && heartbeat.Session != nil {
		session = strings.TrimSpace(*heartbeat.Session)
	}
	mainKey := ""
	if oc != nil && oc.connector != nil && oc.connector.Config.Session != nil {
		mainKey = strings.TrimSpace(oc.connector.Config.Session.MainKey)
	}
	if session == "" || strings.EqualFold(session, "main") || strings.EqualFold(session, "global") || (mainKey != "" && strings.EqualFold(session, mainKey)) {
		hbSession := oc.resolveHeartbeatSession(agentID, heartbeat)
		if portal := oc.heartbeatSessionPortalCandidate(agentID, hbSession); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		if portal := oc.lastActivePortal(agentID); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		if portal := oc.defaultChatPortal(); portal != nil {
			return portal, portal.MXID.String(), nil
		}
		return nil, "", errors.New("no session")
	}
	if strings.HasPrefix(session, "!") {
		if portal := oc.portalByRoomID(context.Background(), id.RoomID(session)); portal != nil {
			if meta := portalMeta(portal); meta == nil || normalizeAgentID(resolveAgentID(meta)) == normalizeAgentID(agentID) {
				return portal, portal.MXID.String(), nil
			}
		}
	}
	hbSession := oc.resolveHeartbeatSession(agentID, heartbeat)
	if portal := oc.heartbeatSessionPortalCandidate(agentID, hbSession); portal != nil {
		return portal, portal.MXID.String(), nil
	}
	if portal := oc.lastActivePortal(agentID); portal != nil {
		return portal, portal.MXID.String(), nil
	}
	if portal := oc.defaultChatPortal(); portal != nil {
		return portal, portal.MXID.String(), nil
	}
	return nil, "", errors.New("no session")
}

func (oc *AIClient) heartbeatSessionPortalCandidate(agentID string, session heartbeatSessionResolution) *bridgev2.Portal {
	if session.Entry == nil {
		return nil
	}
	lastChannel := strings.TrimSpace(session.Entry.LastChannel)
	lastTo := strings.TrimSpace(session.Entry.LastTo)
	if lastTo == "" || !strings.HasPrefix(lastTo, "!") || (lastChannel != "" && !strings.EqualFold(lastChannel, "matrix")) {
		return nil
	}
	portal := oc.portalByRoomID(context.Background(), id.RoomID(lastTo))
	if portal == nil {
		return nil
	}
	if meta := portalMeta(portal); meta != nil && normalizeAgentID(resolveAgentID(meta)) != normalizeAgentID(agentID) {
		return nil
	}
	return portal
}

func (oc *AIClient) shouldRunHeartbeatForFile(agentID string, reason string) bool {
	db := oc.bridgeDB()
	if db == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return true
	}
	store := textfs.NewStore(db, string(oc.UserLogin.Bridge.DB.BridgeID), string(oc.UserLogin.ID), normalizeAgentID(agentID))
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
		if oc == nil {
			return time.Local
		}
		_, loc := oc.resolveUserTimezone()
		return loc
	}
	if strings.EqualFold(trimmed, "local") {
		return time.Local
	}
	if loc, err := time.LoadLocation(trimmed); err == nil {
		return loc
	}
	if oc == nil {
		return time.Local
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
	if strings.HasPrefix(lowered, "read heartbeat.md") {
		return ""
	}
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
