package dummybridge

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseSharedStreamOption(key, value string, hasValue bool, token string, opts *sharedStreamOptions) (bool, error) {
	switch key {
	case "profile":
		if !hasValue {
			return false, fmt.Errorf("%s requires a value", token)
		}
		p := strings.TrimSpace(strings.ToLower(value))
		switch p {
		case "balanced", "tools", "artifacts", "terminals":
			opts.Profile = p
		default:
			return false, fmt.Errorf("unknown profile %q", value)
		}
	case "seed":
		if !hasValue {
			return false, fmt.Errorf("%s requires a value", token)
		}
		s, err := parseInt64(value, "seed")
		if err != nil {
			return false, err
		}
		opts.Seed = s
		opts.SeedSet = true
	case "allow-abort":
		opts.AllowAbort = true
	case "allow-error":
		opts.AllowError = true
	case "allow-approval":
		opts.AllowApproval = true
	default:
		return false, nil
	}
	return true, nil
}

func parseCommonOptions(tokens []string) (commonCommandOptions, error) {
	opts := commonCommandOptions{
		DelayMin:     30 * time.Millisecond,
		DelayMax:     150 * time.Millisecond,
		ChunkMin:     defaultChunkMin,
		ChunkMax:     defaultChunkMax,
		FinishReason: "stop",
	}
	for _, token := range tokens {
		key, value, hasValue := parseOptionToken(token)
		switch key {
		case "reasoning":
			n, err := parseValidatedInt(value, hasValue, token, "reasoning", maxDemoReasoningChars, true)
			if err != nil {
				return opts, err
			}
			opts.ReasoningChars = n
		case "steps":
			n, err := parseValidatedInt(value, hasValue, token, "steps", maxDemoSteps, false)
			if err != nil {
				return opts, err
			}
			opts.Steps = n
		case "sources":
			n, err := parseValidatedInt(value, hasValue, token, "sources", maxDemoCollections, true)
			if err != nil {
				return opts, err
			}
			opts.Sources = n
		case "documents":
			n, err := parseValidatedInt(value, hasValue, token, "documents", maxDemoCollections, true)
			if err != nil {
				return opts, err
			}
			opts.Documents = n
		case "files":
			n, err := parseValidatedInt(value, hasValue, token, "files", maxDemoCollections, true)
			if err != nil {
				return opts, err
			}
			opts.Files = n
		case "meta":
			opts.Meta = true
		case "data":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			opts.DataName = strings.TrimSpace(value)
		case "data-transient":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			opts.DataTransientName = strings.TrimSpace(value)
		case "delay-ms":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return opts, err
			}
			if err := validateMaxDurationRange(minDelay, maxDelay, maxDemoDelay, "delay range"); err != nil {
				return opts, err
			}
			opts.DelayMin, opts.DelayMax = minDelay, maxDelay
		case "chunk-chars":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			minChunk, maxChunk, err := parseIntRange(value, "chunk-chars")
			if err != nil {
				return opts, err
			}
			if err := validateMaxIntRange(minChunk, maxChunk, maxDemoChunkChars, "chunk size range"); err != nil {
				return opts, err
			}
			opts.ChunkMin, opts.ChunkMax = minChunk, maxChunk
		case "seed":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			seed, err := parseInt64(value, "seed")
			if err != nil {
				return opts, err
			}
			opts.Seed = seed
			opts.SeedSet = true
		case "finish":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value", token)
			}
			reason := normalizeFinishReason(value)
			if reason == "" {
				return opts, fmt.Errorf("unsupported finish reason %q", value)
			}
			opts.FinishReason = reason
		case "abort":
			opts.Abort = true
		case "error":
			opts.Error = true
		default:
			return opts, fmt.Errorf("unknown option %q", token)
		}
	}
	if err := validateCommonOptions(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

func validateCommonOptions(opts commonCommandOptions) error {
	finishReason := strings.TrimSpace(opts.FinishReason)
	if finishReason == "" {
		finishReason = "stop"
	}
	if opts.Abort && opts.Error {
		return fmt.Errorf("--abort and --error cannot be combined")
	}
	if (opts.Abort || opts.Error) && finishReason != "stop" {
		return fmt.Errorf("--finish cannot be combined with --abort or --error")
	}
	if opts.ChunkMin <= 0 || opts.ChunkMax < opts.ChunkMin {
		return fmt.Errorf("invalid chunk size range %d:%d", opts.ChunkMin, opts.ChunkMax)
	}
	if opts.DelayMin < 0 || opts.DelayMax < opts.DelayMin {
		return fmt.Errorf("invalid delay range %s:%s", opts.DelayMin, opts.DelayMax)
	}
	if err := validateMaxIntValue(opts.ReasoningChars, maxDemoReasoningChars, "reasoning"); err != nil {
		return err
	}
	if err := validateMaxIntValue(opts.Steps, maxDemoSteps, "steps"); err != nil {
		return err
	}
	if err := validateMaxIntValue(opts.Sources, maxDemoCollections, "sources"); err != nil {
		return err
	}
	if err := validateMaxIntValue(opts.Documents, maxDemoCollections, "documents"); err != nil {
		return err
	}
	if err := validateMaxIntValue(opts.Files, maxDemoCollections, "files"); err != nil {
		return err
	}
	if err := validateMaxIntRange(opts.ChunkMin, opts.ChunkMax, maxDemoChunkChars, "chunk size range"); err != nil {
		return err
	}
	return validateMaxDurationRange(opts.DelayMin, opts.DelayMax, maxDemoDelay, "delay range")
}

func validateMaxIntValue(value, max int, label string) error {
	if value > max {
		return fmt.Errorf("%s %d exceeds the maximum of %d", label, value, max)
	}
	return nil
}

func validateMaxIntRange(minValue, maxValue, max int, label string) error {
	if minValue > max || maxValue > max {
		return fmt.Errorf("invalid %s %d:%d; maximum is %d", label, minValue, maxValue, max)
	}
	return nil
}

func validateMaxDurationRange(minValue, maxValue, max time.Duration, label string) error {
	if minValue > max || maxValue > max {
		return fmt.Errorf("invalid %s %s:%s; maximum is %s", label, minValue, maxValue, max)
	}
	return nil
}

func parseToolSpec(raw string, idx int) (toolSpec, error) {
	parts := strings.Split(raw, "#")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return toolSpec{}, fmt.Errorf("tool spec %q is missing a tool name", raw)
	}
	spec := toolSpec{
		Name:          name,
		Tags:          make([]string, 0, len(parts)-1),
		DisplayTitle:  name,
		SequenceIndex: idx + 1,
	}
	for _, tag := range parts[1:] {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag == "" {
			continue
		}
		spec.Tags = append(spec.Tags, tag)
		switch tag {
		case "fail":
			spec.Fail = true
		case "approval":
			spec.Approval = true
		case "deny":
			spec.Deny = true
		case "delta":
			spec.Delta = true
		case "inputerror":
			spec.InputError = true
		case "prelim":
			spec.Preliminary = true
		case "provider":
			spec.Provider = true
		default:
			return toolSpec{}, fmt.Errorf("unknown tool tag %q in %q", tag, raw)
		}
	}
	finalStates := 0
	if spec.Fail {
		finalStates++
	}
	if spec.Approval {
		finalStates++
	}
	if spec.Deny {
		finalStates++
	}
	if finalStates > 1 {
		return toolSpec{}, fmt.Errorf("tool spec %q has conflicting final state tags", raw)
	}
	return spec, nil
}

func normalizeFinishReason(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "stop":
		return "stop"
	case "length":
		return "length"
	case "tool-calls", "tool_calls", "toolcalls":
		return "tool-calls"
	case "content-filter", "content_filter", "contentfilter":
		return "content-filter"
	case "other":
		return "other"
	default:
		return ""
	}
}

func parseOptionToken(token string) (string, string, bool) {
	trimmed := strings.TrimSpace(token)
	trimmed = strings.TrimPrefix(trimmed, "--")
	key, value, ok := strings.Cut(trimmed, "=")
	return strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value), ok
}

func parseValidatedInt(value string, hasValue bool, token, label string, max int, allowZero bool) (int, error) {
	if !hasValue {
		return 0, fmt.Errorf("%s requires a value", token)
	}
	var n int
	var err error
	if allowZero {
		n, err = parseNonNegativeInt(value, label)
	} else {
		n, err = parsePositiveInt(value, label)
	}
	if err != nil {
		return 0, err
	}
	if err := validateMaxIntValue(n, max, label); err != nil {
		return 0, err
	}
	return n, nil
}

func parsePositiveInt(raw string, label string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s %q", label, raw)
	}
	return value, nil
}

func parseNonNegativeInt(raw string, label string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s %q", label, raw)
	}
	return value, nil
}

func parseInt64(raw string, label string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q", label, raw)
	}
	return value, nil
}

func parseDurationRangeMS(raw string) (time.Duration, time.Duration, error) {
	minValue, maxValue, err := parseIntRange(raw, "delay-ms")
	if err != nil {
		return 0, 0, err
	}
	return time.Duration(minValue) * time.Millisecond, time.Duration(maxValue) * time.Millisecond, nil
}

func parseIntRange(raw string, label string) (int, int, error) {
	minRaw, maxRaw, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		value, err := parseNonNegativeInt(raw, label)
		if err != nil {
			return 0, 0, err
		}
		return value, value, nil
	}
	minValue, err := parseNonNegativeInt(minRaw, label)
	if err != nil {
		return 0, 0, err
	}
	maxValue, err := parseNonNegativeInt(maxRaw, label)
	if err != nil {
		return 0, 0, err
	}
	if maxValue < minValue {
		return 0, 0, fmt.Errorf("invalid %s range %q", label, raw)
	}
	return minValue, maxValue, nil
}

func futureDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
