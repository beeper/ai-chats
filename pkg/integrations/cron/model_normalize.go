package cron

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"
)

var (
	agentIDValidRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	agentIDInvalidChars = regexp.MustCompile(`[^a-z0-9_-]+`)
	agentIDLeadingDash  = regexp.MustCompile(`^-+`)
	agentIDTrailingDash = regexp.MustCompile(`-+$`)
	allowedCronJobKeys  = map[string]struct{}{
		"agentId":        {},
		"name":           {},
		"description":    {},
		"enabled":        {},
		"deleteAfterRun": {},
		"schedule":       {},
		"payload":        {},
		"delivery":       {},
		"state":          {},
	}
	allowedCronScheduleKeys = map[string]struct{}{
		"kind":     {},
		"at":       {},
		"everyMs":  {},
		"anchorMs": {},
		"expr":     {},
		"tz":       {},
	}
	allowedCronPayloadKeys = map[string]struct{}{
		"kind":                       {},
		"message":                    {},
		"model":                      {},
		"thinking":                   {},
		"timeoutSeconds":             {},
		"allowUnsafeExternalContent": {},
	}
	allowedCronDeliveryKeys = map[string]struct{}{
		"mode":       {},
		"channel":    {},
		"to":         {},
		"bestEffort": {},
	}
)

const defaultAgentID = "main"

func sanitizeAgentID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultAgentID
	}
	lowered := strings.ToLower(trimmed)
	if agentIDValidRe.MatchString(lowered) {
		return lowered
	}
	cleaned := agentIDInvalidChars.ReplaceAllString(lowered, "-")
	cleaned = agentIDLeadingDash.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimLeft(cleaned, "_")
	cleaned = agentIDTrailingDash.ReplaceAllString(cleaned, "")
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
		cleaned = strings.TrimLeft(cleaned, "_")
		cleaned = agentIDTrailingDash.ReplaceAllString(cleaned, "")
	}
	if cleaned == "" || !agentIDValidRe.MatchString(cleaned) {
		return defaultAgentID
	}
	return cleaned
}

func NormalizeJobCreate(input JobCreate) JobCreate {
	next := input
	if next.Enabled == nil {
		enabled := true
		next.Enabled = &enabled
	}
	if next.DeleteAfterRun == nil && strings.EqualFold(strings.TrimSpace(next.Schedule.Kind), "at") {
		deleteAfter := true
		next.DeleteAfterRun = &deleteAfter
	}
	if next.Delivery == nil {
		if normalizeString(next.Payload.Kind) == "agentturn" {
			next.Delivery = &Delivery{Mode: DeliveryAnnounce}
		}
	}
	return next
}

func NormalizeJobCreateRaw(raw any) (JobCreate, error) {
	normalized := normalizeCronJobInputRaw(raw, true)
	if normalized == nil {
		return JobCreate{}, errors.New("normalize create: unsupported or invalid cron job input")
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return JobCreate{}, fmt.Errorf("normalize create marshal: %w", err)
	}
	var out JobCreate
	if err := json.Unmarshal(data, &out); err != nil {
		return JobCreate{}, fmt.Errorf("normalize create unmarshal: %w", err)
	}
	return NormalizeJobCreate(out), nil
}

func NormalizeJobPatchRaw(raw any) (JobPatch, error) {
	normalized := normalizeCronJobInputRaw(raw, false)
	if normalized == nil {
		return JobPatch{}, errors.New("normalize patch: unsupported or invalid cron job input")
	}
	agentIDPresent := false
	agentIDNil := false
	if val, ok := normalized["agentId"]; ok {
		agentIDPresent = true
		agentIDNil = val == nil
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return JobPatch{}, fmt.Errorf("normalize patch marshal: %w", err)
	}
	var out JobPatch
	if err := json.Unmarshal(data, &out); err != nil {
		return JobPatch{}, fmt.Errorf("normalize patch unmarshal: %w", err)
	}
	if agentIDPresent && agentIDNil && out.AgentID == nil {
		empty := ""
		out.AgentID = &empty
	}
	return out, nil
}

func normalizeCronJobInputRaw(raw any, applyDefaults bool) map[string]any {
	base, ok := unwrapCronJob(raw)
	if !ok {
		return nil
	}
	if !hasOnlyAllowedKeys(base, allowedCronJobKeys) {
		return nil
	}
	next := maps.Clone(base)

	if val, ok := base["agentId"]; ok {
		switch v := val.(type) {
		case nil:
			next["agentId"] = nil
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				if applyDefaults {
					delete(next, "agentId")
				} else {
					next["agentId"] = ""
				}
			} else {
				next["agentId"] = sanitizeAgentID(trimmed)
			}
		}
	}
	if val, ok := base["enabled"]; ok {
		switch v := val.(type) {
		case bool:
			next["enabled"] = v
		case string:
			trimmed := normalizeString(v)
			if trimmed == "true" {
				next["enabled"] = true
			} else if trimmed == "false" {
				next["enabled"] = false
			}
		}
	}
	if schedRaw, ok := base["schedule"]; ok {
		if schedMap, ok := schedRaw.(map[string]any); ok {
			if !hasOnlyAllowedKeys(schedMap, allowedCronScheduleKeys) {
				return nil
			}
			next["schedule"] = coerceScheduleMap(schedMap)
		}
	}
	if deliveryRaw, ok := base["delivery"]; ok {
		if deliveryMap, ok := deliveryRaw.(map[string]any); ok {
			if !hasOnlyAllowedKeys(deliveryMap, allowedCronDeliveryKeys) {
				return nil
			}
			next["delivery"] = coerceDeliveryMap(deliveryMap)
		}
	}
	if payloadRaw, ok := base["payload"]; ok {
		if payloadMap, ok := payloadRaw.(map[string]any); ok {
			if !hasOnlyAllowedKeys(payloadMap, allowedCronPayloadKeys) {
				return nil
			}
			next["payload"] = maps.Clone(payloadMap)
		}
	}
	if applyDefaults {
		if payloadMap, ok := next["payload"].(map[string]any); ok {
			payloadKind := ""
			if kind, ok := payloadMap["kind"].(string); ok {
				payloadKind = normalizeString(kind)
			}
			if _, hasDelivery := next["delivery"]; !hasDelivery && payloadKind == "agentturn" {
				next["delivery"] = map[string]any{"mode": "announce"}
			}
		}
	}
	return next
}

func hasOnlyAllowedKeys(input map[string]any, allowed map[string]struct{}) bool {
	for key := range input {
		if _, ok := allowed[key]; !ok {
			return false
		}
	}
	return true
}

func unwrapCronJob(raw any) (map[string]any, bool) {
	base, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	if job, ok := base["job"].(map[string]any); ok {
		return job, true
	}
	return base, true
}

func coerceScheduleMap(schedule map[string]any) map[string]any {
	next := maps.Clone(schedule)
	kind, _ := schedule["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		switch {
		case schedule["at"] != nil:
			next["kind"] = "at"
		case schedule["everyMs"] != nil:
			next["kind"] = "every"
		case schedule["expr"] != nil:
			next["kind"] = "cron"
		}
	}
	if atVal, ok := coerceScheduleAt(schedule); ok {
		next["at"] = atVal
	}
	return next
}

func coerceScheduleAt(schedule map[string]any) (string, bool) {
	if rawAt, ok := schedule["at"].(string); ok {
		trimmed := strings.TrimSpace(rawAt)
		if trimmed != "" {
			if ts, ok := parseAbsoluteTimeMs(trimmed); ok {
				return time.UnixMilli(ts).UTC().Format("2006-01-02T15:04:05.000Z"), true
			}
			return trimmed, true
		}
	}
	return "", false
}

func coerceDeliveryMap(delivery map[string]any) map[string]any {
	next := maps.Clone(delivery)
	if rawMode, ok := delivery["mode"].(string); ok {
		mode := normalizeString(rawMode)
		if mode != "" {
			next["mode"] = mode
		} else {
			delete(next, "mode")
		}
	}
	if rawChannel, ok := delivery["channel"].(string); ok {
		channel := normalizeString(rawChannel)
		if channel != "" {
			next["channel"] = channel
		} else {
			delete(next, "channel")
		}
	}
	if rawTo, ok := delivery["to"].(string); ok {
		to := strings.TrimSpace(rawTo)
		if to != "" {
			next["to"] = to
		} else {
			delete(next, "to")
		}
	}
	return next
}
