package cron

import (
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"
)

type rawRecord map[string]any

func normalizeCronJobInputRaw(raw any, applyDefaults bool) rawRecord {
	base, ok := unwrapCronJob(raw)
	if !ok {
		return nil
	}
	next := maps.Clone(base)

	// agentId handling (trim, allow null to clear)
	if val, ok := base["agentId"]; ok {
		switch v := val.(type) {
		case nil:
			next["agentId"] = nil
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				delete(next, "agentId")
			} else {
				next["agentId"] = sanitizeAgentID(trimmed)
			}
		}
	}

	// enabled coercion
	if val, ok := base["enabled"]; ok {
		switch v := val.(type) {
		case bool:
			next["enabled"] = v
		case string:
			trimmed := strings.ToLower(strings.TrimSpace(v))
			if trimmed == "true" {
				next["enabled"] = true
			} else if trimmed == "false" {
				next["enabled"] = false
			}
		}
	}

	// schedule coercion
	if schedRaw, ok := base["schedule"]; ok {
		if schedMap, ok := schedRaw.(map[string]any); ok {
			next["schedule"] = coerceScheduleMap(schedMap)
		}
	}
	if deliveryRaw, ok := base["delivery"]; ok {
		if deliveryMap, ok := deliveryRaw.(map[string]any); ok {
			next["delivery"] = coerceDeliveryMap(deliveryMap)
		}
	}
	if payloadRaw, ok := base["payload"]; ok {
		if payloadMap, ok := payloadRaw.(map[string]any); ok {
			next["payload"] = maps.Clone(payloadMap)
		}
	}
	if _, ok := base["isolation"]; ok {
		delete(next, "isolation")
	}

	if applyDefaults {
		if _, ok := next["wakeMode"]; !ok {
			next["wakeMode"] = string(CronWakeNextHeartbeat)
		}
		if _, ok := next["sessionTarget"]; !ok {
			if payloadMap, ok := next["payload"].(map[string]any); ok {
				if kind, ok := payloadMap["kind"].(string); ok {
					switch strings.ToLower(strings.TrimSpace(kind)) {
					case "systemevent":
						next["sessionTarget"] = string(CronSessionMain)
					case "agentturn":
						next["sessionTarget"] = string(CronSessionIsolated)
					}
				}
			}
		}
		if payloadMap, ok := next["payload"].(map[string]any); ok {
			payloadKind := ""
			if kind, ok := payloadMap["kind"].(string); ok {
				payloadKind = strings.ToLower(strings.TrimSpace(kind))
			}
			sessionTarget := ""
			if target, ok := next["sessionTarget"].(string); ok {
				sessionTarget = strings.ToLower(strings.TrimSpace(target))
			}
			isIsolatedAgentTurn := sessionTarget == "isolated" || (sessionTarget == "" && payloadKind == "agentturn")
			_, hasDelivery := next["delivery"]
			if !hasDelivery && isIsolatedAgentTurn && payloadKind == "agentturn" {
				next["delivery"] = map[string]any{"mode": "announce"}
			}
		}
	}

	return next
}

func unwrapCronJob(raw any) (rawRecord, bool) {
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
		if schedule["at"] != nil {
			next["kind"] = "at"
		} else if schedule["atMs"] != nil {
			next["kind"] = "at"
		} else if schedule["everyMs"] != nil {
			next["kind"] = "every"
		} else if schedule["expr"] != nil {
			next["kind"] = "cron"
		}
	}

	if atVal, ok := coerceScheduleAt(schedule); ok {
		next["at"] = atVal
		delete(next, "atMs")
	}
	return next
}

func coerceScheduleAt(schedule map[string]any) (string, bool) {
	if rawAt, ok := schedule["at"].(string); ok {
		trimmed := strings.TrimSpace(rawAt)
		if trimmed != "" {
			if ts, ok := parseAbsoluteTimeMs(trimmed); ok {
				return formatIsoMillis(ts), true
			}
			return trimmed, true
		}
	}
	if ts, ok := coerceAtMs(schedule["atMs"]); ok {
		return formatIsoMillis(ts), true
	}
	return "", false
}

func coerceAtMs(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case int:
		return int64(v), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, false
		}
		if ts, ok := parseAbsoluteTimeMs(trimmed); ok {
			return ts, true
		}
	}
	return 0, false
}

func formatIsoMillis(ts int64) string {
	return time.UnixMilli(ts).UTC().Format("2006-01-02T15:04:05.000Z")
}

func coerceDeliveryMap(delivery map[string]any) map[string]any {
	next := maps.Clone(delivery)
	if rawMode, ok := delivery["mode"].(string); ok {
		mode := strings.ToLower(strings.TrimSpace(rawMode))
		if mode != "" {
			next["mode"] = mode
		} else {
			delete(next, "mode")
		}
	}
	if rawChannel, ok := delivery["channel"].(string); ok {
		channel := strings.ToLower(strings.TrimSpace(rawChannel))
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

// NormalizeCronJobCreateRaw normalizes raw input into a CronJobCreate.
func NormalizeCronJobCreateRaw(raw any) (CronJobCreate, error) {
	normalized := normalizeCronJobInputRaw(raw, true)
	if normalized == nil {
		return CronJobCreate{}, errors.New("invalid cron job")
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return CronJobCreate{}, err
	}
	var out CronJobCreate
	if err := json.Unmarshal(data, &out); err != nil {
		return CronJobCreate{}, err
	}
	return NormalizeCronJobCreate(out), nil
}

// NormalizeCronJobPatchRaw normalizes raw input into a CronJobPatch.
func NormalizeCronJobPatchRaw(raw any) (CronJobPatch, error) {
	normalized := normalizeCronJobInputRaw(raw, false)
	if normalized == nil {
		return CronJobPatch{}, errors.New("invalid cron patch")
	}
	agentIDPresent := false
	agentIDNil := false
	if val, ok := normalized["agentId"]; ok {
		agentIDPresent = true
		agentIDNil = val == nil
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return CronJobPatch{}, err
	}
	var out CronJobPatch
	if err := json.Unmarshal(data, &out); err != nil {
		return CronJobPatch{}, err
	}
	if agentIDPresent && agentIDNil && out.AgentID == nil {
		empty := ""
		out.AgentID = &empty
	}
	return NormalizeCronJobPatch(out), nil
}
