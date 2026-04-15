package dummybridge

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beeper/agentremote/sdk"
)

func (dc *DummyBridgeConnector) onMessage(session *dummySession, conv *sdk.Conversation, msg *sdk.Message, turn *sdk.Turn) error {
	if conv == nil || turn == nil || msg == nil {
		return nil
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return sdk.SendSystemMessage(turn.Context(), conv.Login(), conv.Portal(), conv.Sender(), helpText())
	}
	cmd, err := parseCommand(text)
	if err != nil {
		return sdk.SendSystemMessage(turn.Context(), conv.Login(), conv.Portal(), conv.Sender(), fmt.Sprintf("%s\n\n%s", err.Error(), helpText()))
	}
	if cmd == nil {
		return sdk.SendSystemMessage(turn.Context(), conv.Login(), conv.Portal(), conv.Sender(), helpText())
	}
	if cmd.Name == "help" {
		return sdk.SendSystemMessage(turn.Context(), conv.Login(), conv.Portal(), conv.Sender(), helpText())
	}
	if session == nil {
		return errors.New("dummybridge session is unavailable")
	}
	log := session.log.With().Str("command", cmd.Name).Str("turn_id", turn.ID()).Logger()
	runner := demoRunner{runtime: defaultDemoRuntime()}
	started := runner.runtime.now()
	var runErr error
	switch {
	case cmd.Lorem != nil:
		runErr = runner.runLorem(turn.Context(), turn, *cmd.Lorem, log)
	case cmd.Tools != nil:
		runErr = runner.runTools(turn.Context(), turn, *cmd.Tools, log)
	case cmd.Random != nil:
		runErr = runner.runRandom(turn.Context(), turn, *cmd.Random, log)
	case cmd.Chaos != nil:
		runErr = runner.runChaos(turn.Context(), conv, turn, *cmd.Chaos, log)
	default:
		runErr = sdk.SendSystemMessage(turn.Context(), conv.Login(), conv.Portal(), conv.Sender(), helpText())
	}
	if runErr != nil {
		log.Warn().Err(runErr).Dur("elapsed", runner.runtime.now().Sub(started)).Msg("DummyBridge demo command failed")
	}
	return runErr
}

func parseCommand(input string) (*parsedCommand, error) {
	tokens := strings.Fields(strings.TrimSpace(input))
	if len(tokens) == 0 {
		return nil, nil
	}
	switch strings.ToLower(tokens[0]) {
	case "help", "/help", "!help":
		return &parsedCommand{Name: "help"}, nil
	case "dummybridge":
		if len(tokens) > 1 && strings.EqualFold(tokens[1], "help") {
			return &parsedCommand{Name: "help"}, nil
		}
		return nil, nil
	case "stream-lorem":
		cmd, err := parseLoremCommand(tokens[1:])
		if err != nil {
			return nil, err
		}
		return &parsedCommand{Name: "stream-lorem", Lorem: cmd}, nil
	case "stream-tools":
		cmd, err := parseToolsCommand(tokens[1:])
		if err != nil {
			return nil, err
		}
		return &parsedCommand{Name: "stream-tools", Tools: cmd}, nil
	case "stream-random":
		cmd, err := parseRandomCommand(tokens[1:])
		if err != nil {
			return nil, err
		}
		return &parsedCommand{Name: "stream-random", Random: cmd}, nil
	case "stream-chaos":
		cmd, err := parseChaosCommand(tokens[1:])
		if err != nil {
			return nil, err
		}
		return &parsedCommand{Name: "stream-chaos", Chaos: cmd}, nil
	default:
		return nil, nil
	}
}

func helpText() string {
	return strings.Join([]string{
		"DummyBridge demo commands:",
		"help",
		"stream-lorem <chars> [--reasoning=N] [--steps=N] [--sources=N] [--documents=N] [--files=N] [--meta] [--data=name] [--data-transient=name] [--delay-ms=min:max] [--chunk-chars=min:max] [--seed=N] [--finish=stop|length|tool-calls|content-filter|other] [--abort|--error]",
		"stream-tools <chars> <tool[#fail|#approval|#deny|#delta|#inputerror|#prelim|#provider]>... [common options]",
		"stream-random [seconds] [--actions=N] [--profile=balanced|tools|artifacts|terminals] [--seed=N] [--delay-ms=min:max] [--allow-abort] [--allow-error] [--allow-approval]",
		"stream-chaos [turns] [seconds] [--profile=balanced|tools|artifacts|terminals] [--seed=N] [--stagger-ms=min:max] [--max-actions=N] [--allow-abort] [--allow-error] [--allow-approval]",
		"Notes: plain messages only, new chats create new rooms, and approval-tagged tools wait for user approval.",
	}, "\n")
}

func parseLoremCommand(tokens []string) (*loremCommand, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("stream-lorem requires a character count")
	}
	count, err := parsePositiveInt(tokens[0], "character count")
	if err != nil {
		return nil, err
	}
	if err := validateMaxIntValue(count, maxDemoChars, "character count"); err != nil {
		return nil, err
	}
	opts, err := parseCommonOptions(tokens[1:])
	if err != nil {
		return nil, err
	}
	return &loremCommand{Chars: count, Options: opts}, nil
}

func parseToolsCommand(tokens []string) (*toolsCommand, error) {
	if len(tokens) < 2 {
		return nil, fmt.Errorf("stream-tools requires a character count and at least one tool")
	}
	count, err := parsePositiveInt(tokens[0], "character count")
	if err != nil {
		return nil, err
	}
	if err := validateMaxIntValue(count, maxDemoChars, "character count"); err != nil {
		return nil, err
	}
	toolTokens := make([]string, 0, len(tokens))
	optTokens := make([]string, 0, len(tokens))
	for _, token := range tokens[1:] {
		if strings.HasPrefix(token, "--") {
			optTokens = append(optTokens, token)
		} else {
			toolTokens = append(toolTokens, token)
		}
	}
	if len(toolTokens) == 0 {
		return nil, fmt.Errorf("stream-tools requires at least one tool spec")
	}
	if err := validateMaxIntValue(len(toolTokens), maxDemoToolSpecs, "tool spec count"); err != nil {
		return nil, err
	}
	opts, err := parseCommonOptions(optTokens)
	if err != nil {
		return nil, err
	}
	tools := make([]toolSpec, 0, len(toolTokens))
	for idx, token := range toolTokens {
		spec, err := parseToolSpec(token, idx)
		if err != nil {
			return nil, err
		}
		tools = append(tools, spec)
	}
	return &toolsCommand{Chars: count, Tools: tools, Options: opts}, nil
}

func parseRandomCommand(tokens []string) (*randomCommand, error) {
	cmd := &randomCommand{
		Duration:            20 * time.Second,
		Actions:             20,
		DelayMin:            350 * time.Millisecond,
		DelayMax:            1150 * time.Millisecond,
		sharedStreamOptions: sharedStreamOptions{Profile: "balanced"},
	}
	rest := tokens
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		seconds, err := parsePositiveInt(rest[0], "duration")
		if err != nil {
			return nil, err
		}
		if err := validateMaxIntValue(seconds, maxDemoDurationSeconds, "duration seconds"); err != nil {
			return nil, err
		}
		cmd.Duration = futureDuration(seconds)
		rest = rest[1:]
	}
	for _, token := range rest {
		key, value, hasValue := parseOptionToken(token)
		switch key {
		case "actions":
			n, err := parseValidatedInt(value, hasValue, token, "actions", maxDemoRandomActions, false)
			if err != nil {
				return nil, err
			}
			cmd.Actions = n
		case "delay-ms":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return nil, err
			}
			if err := validateMaxDurationRange(minDelay, maxDelay, maxDemoDelay, "delay range"); err != nil {
				return nil, err
			}
			cmd.DelayMin, cmd.DelayMax = minDelay, maxDelay
		default:
			handled, err := parseSharedStreamOption(key, value, hasValue, token, &cmd.sharedStreamOptions)
			if err != nil {
				return nil, err
			}
			if !handled {
				return nil, fmt.Errorf("unknown random option %q", token)
			}
		}
	}
	return cmd, nil
}

func parseChaosCommand(tokens []string) (*chaosCommand, error) {
	cmd := &chaosCommand{
		Turns:               3,
		Duration:            10 * time.Second,
		StaggerMin:          150 * time.Millisecond,
		StaggerMax:          900 * time.Millisecond,
		MaxActions:          10,
		sharedStreamOptions: sharedStreamOptions{Profile: "balanced"},
	}
	rest := tokens
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		n, err := parsePositiveInt(rest[0], "turn count")
		if err != nil {
			return nil, err
		}
		if err := validateMaxIntValue(n, maxDemoChaosTurns, "turn count"); err != nil {
			return nil, err
		}
		cmd.Turns = n
		rest = rest[1:]
	}
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		seconds, err := parsePositiveInt(rest[0], "duration")
		if err != nil {
			return nil, err
		}
		if err := validateMaxIntValue(seconds, maxDemoDurationSeconds, "duration seconds"); err != nil {
			return nil, err
		}
		cmd.Duration = futureDuration(seconds)
		rest = rest[1:]
	}
	for _, token := range rest {
		key, value, hasValue := parseOptionToken(token)
		switch key {
		case "stagger-ms":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return nil, err
			}
			if err := validateMaxDurationRange(minDelay, maxDelay, maxDemoStagger, "stagger range"); err != nil {
				return nil, err
			}
			cmd.StaggerMin, cmd.StaggerMax = minDelay, maxDelay
		case "max-actions":
			n, err := parseValidatedInt(value, hasValue, token, "max-actions", maxDemoChaosActions, false)
			if err != nil {
				return nil, err
			}
			cmd.MaxActions = n
		default:
			handled, err := parseSharedStreamOption(key, value, hasValue, token, &cmd.sharedStreamOptions)
			if err != nil {
				return nil, err
			}
			if !handled {
				return nil, fmt.Errorf("unknown chaos option %q", token)
			}
		}
	}
	if cmd.Turns < 1 {
		return nil, fmt.Errorf("stream-chaos requires at least one turn")
	}
	return cmd, nil
}

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
	if err := validateMaxDurationRange(opts.DelayMin, opts.DelayMax, maxDemoDelay, "delay range"); err != nil {
		return err
	}
	return nil
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
