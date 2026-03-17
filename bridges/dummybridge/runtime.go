package dummybridge

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const (
	defaultChunkMin = 24
	defaultChunkMax = 96
)

var loremSentenceCorpus = []string{
	"Lorem ipsum dolor sit amet, consectetur adipiscing elit.",
	"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.",
	"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.",
	"Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.",
	"Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.",
	"Integer nec odio praesent libero sed cursus ante dapibus diam.",
	"Nulla quis sem at nibh elementum imperdiet duis sagittis ipsum.",
	"Praesent mauris fusce nec tellus sed augue semper porta.",
	"Mauris massa vestibulum lacinia arcu eget nulla.",
	"Class aptent taciti sociosqu ad litora torquent per conubia nostra.",
	"In consectetur orci eu erat varius, vitae facilisis lorem blandit.",
	"Curabitur ullamcorper ultricies nisi nam eget dui etiam rhoncus.",
	"Donec sodales sagittis magna sed consequat leo eget bibendum sodales.",
	"Aliquam lorem ante dapibus in viverra quis feugiat a tellus.",
	"Phasellus viverra nulla ut metus varius laoreet quisque rutrum.",
}

type commonCommandOptions struct {
	ReasoningChars    int
	Steps             int
	Sources           int
	Documents         int
	Files             int
	Meta              bool
	DataName          string
	DataTransientName string
	DelayMin          time.Duration
	DelayMax          time.Duration
	ChunkMin          int
	ChunkMax          int
	FinishReason      string
	Abort             bool
	Error             bool
	Seed              int64
	SeedSet           bool
}

type loremCommand struct {
	Chars   int
	Options commonCommandOptions
}

type toolSpec struct {
	Name          string
	Tags          []string
	Fail          bool
	Approval      bool
	Deny          bool
	Delta         bool
	InputError    bool
	Preliminary   bool
	Provider      bool
	DisplayTitle  string
	SequenceIndex int
}

type toolsCommand struct {
	Chars   int
	Tools   []toolSpec
	Options commonCommandOptions
}

type randomCommand struct {
	Duration      time.Duration
	Actions       int
	Profile       string
	DelayMin      time.Duration
	DelayMax      time.Duration
	Seed          int64
	SeedSet       bool
	AllowAbort    bool
	AllowError    bool
	AllowApproval bool
}

type chaosCommand struct {
	Turns         int
	Duration      time.Duration
	Profile       string
	Seed          int64
	SeedSet       bool
	StaggerMin    time.Duration
	StaggerMax    time.Duration
	MaxActions    int
	AllowAbort    bool
	AllowError    bool
	AllowApproval bool
}

type parsedCommand struct {
	Name   string
	Lorem  *loremCommand
	Tools  *toolsCommand
	Random *randomCommand
	Chaos  *chaosCommand
}

type demoRuntime struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

func defaultDemoRuntime() demoRuntime {
	return demoRuntime{
		now: time.Now,
		sleep: func(ctx context.Context, delay time.Duration) error {
			if delay <= 0 {
				return nil
			}
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

type demoRunner struct {
	runtime demoRuntime
}

type randomActionKind string

const (
	randomActionText        randomActionKind = "text"
	randomActionReasoning   randomActionKind = "reasoning"
	randomActionStep        randomActionKind = "step"
	randomActionToolOK      randomActionKind = "tool_ok"
	randomActionToolFail    randomActionKind = "tool_fail"
	randomActionToolApprove randomActionKind = "tool_approval"
	randomActionToolDeny    randomActionKind = "tool_deny"
	randomActionSource      randomActionKind = "source"
	randomActionDocument    randomActionKind = "document"
	randomActionFile        randomActionKind = "file"
	randomActionMetadata    randomActionKind = "metadata"
	randomActionData        randomActionKind = "data"
	randomActionTransient   randomActionKind = "data_transient"
)

func (dc *DummyBridgeConnector) onMessage(session any, conv *bridgesdk.Conversation, msg *bridgesdk.Message, turn *bridgesdk.Turn) error {
	if conv == nil || turn == nil || msg == nil {
		return nil
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return conv.SendNotice(turn.Context(), helpText())
	}
	cmd, err := parseCommand(text)
	if err != nil {
		return conv.SendNotice(turn.Context(), fmt.Sprintf("%s\n\n%s", err.Error(), helpText()))
	}
	if cmd == nil {
		return conv.SendNotice(turn.Context(), helpText())
	}
	if cmd.Name == "help" {
		return conv.SendNotice(turn.Context(), helpText())
	}
	dummy, err := sessionFromAny(session)
	if err != nil {
		return err
	}
	log := dummy.log.With().Str("command", cmd.Name).Str("turn_id", turn.ID()).Logger()
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
		runErr = conv.SendNotice(turn.Context(), helpText())
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
		return nil, fmt.Errorf("stream-lorem requires a character count.")
	}
	count, err := parsePositiveInt(tokens[0], "character count")
	if err != nil {
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
		return nil, fmt.Errorf("stream-tools requires a character count and at least one tool.")
	}
	count, err := parsePositiveInt(tokens[0], "character count")
	if err != nil {
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
		return nil, fmt.Errorf("stream-tools requires at least one tool spec.")
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
		Duration: 20 * time.Second,
		Actions:  20,
		Profile:  "balanced",
		DelayMin: 350 * time.Millisecond,
		DelayMax: 1150 * time.Millisecond,
	}
	rest := tokens
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		seconds, err := parsePositiveInt(rest[0], "duration")
		if err != nil {
			return nil, err
		}
		cmd.Duration = futureDuration(seconds)
		rest = rest[1:]
	}
	for _, token := range rest {
		key, value, hasValue := parseOptionToken(token)
		switch key {
		case "actions":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parsePositiveInt(value, "actions")
			if err != nil {
				return nil, err
			}
			cmd.Actions = n
		case "profile":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			profile := strings.TrimSpace(strings.ToLower(value))
			switch profile {
			case "balanced", "tools", "artifacts", "terminals":
				cmd.Profile = profile
			default:
				return nil, fmt.Errorf("unknown random profile %q.", value)
			}
		case "delay-ms":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return nil, err
			}
			cmd.DelayMin, cmd.DelayMax = minDelay, maxDelay
		case "seed":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			seed, err := parseInt64(value, "seed")
			if err != nil {
				return nil, err
			}
			cmd.Seed = seed
			cmd.SeedSet = true
		case "allow-abort":
			cmd.AllowAbort = true
		case "allow-error":
			cmd.AllowError = true
		case "allow-approval":
			cmd.AllowApproval = true
		default:
			return nil, fmt.Errorf("unknown random option %q.", token)
		}
	}
	return cmd, nil
}

func parseChaosCommand(tokens []string) (*chaosCommand, error) {
	cmd := &chaosCommand{
		Turns:      3,
		Duration:   10 * time.Second,
		Profile:    "balanced",
		StaggerMin: 150 * time.Millisecond,
		StaggerMax: 900 * time.Millisecond,
		MaxActions: 10,
	}
	rest := tokens
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
		n, err := parsePositiveInt(rest[0], "turn count")
		if err != nil {
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
		cmd.Duration = futureDuration(seconds)
		rest = rest[1:]
	}
	for _, token := range rest {
		key, value, hasValue := parseOptionToken(token)
		switch key {
		case "profile":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			profile := strings.TrimSpace(strings.ToLower(value))
			switch profile {
			case "balanced", "tools", "artifacts", "terminals":
				cmd.Profile = profile
			default:
				return nil, fmt.Errorf("unknown chaos profile %q.", value)
			}
		case "seed":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			seed, err := parseInt64(value, "seed")
			if err != nil {
				return nil, err
			}
			cmd.Seed = seed
			cmd.SeedSet = true
		case "stagger-ms":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return nil, err
			}
			cmd.StaggerMin, cmd.StaggerMax = minDelay, maxDelay
		case "max-actions":
			if !hasValue {
				return nil, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parsePositiveInt(value, "max-actions")
			if err != nil {
				return nil, err
			}
			cmd.MaxActions = n
		case "allow-abort":
			cmd.AllowAbort = true
		case "allow-error":
			cmd.AllowError = true
		case "allow-approval":
			cmd.AllowApproval = true
		default:
			return nil, fmt.Errorf("unknown chaos option %q.", token)
		}
	}
	if cmd.Turns < 1 {
		return nil, fmt.Errorf("stream-chaos requires at least one turn.")
	}
	return cmd, nil
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
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parseNonNegativeInt(value, "reasoning")
			if err != nil {
				return opts, err
			}
			opts.ReasoningChars = n
		case "steps":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parsePositiveInt(value, "steps")
			if err != nil {
				return opts, err
			}
			opts.Steps = n
		case "sources":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parseNonNegativeInt(value, "sources")
			if err != nil {
				return opts, err
			}
			opts.Sources = n
		case "documents":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parseNonNegativeInt(value, "documents")
			if err != nil {
				return opts, err
			}
			opts.Documents = n
		case "files":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			n, err := parseNonNegativeInt(value, "files")
			if err != nil {
				return opts, err
			}
			opts.Files = n
		case "meta":
			opts.Meta = true
		case "data":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			opts.DataName = strings.TrimSpace(value)
		case "data-transient":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			opts.DataTransientName = strings.TrimSpace(value)
		case "delay-ms":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			minDelay, maxDelay, err := parseDurationRangeMS(value)
			if err != nil {
				return opts, err
			}
			opts.DelayMin, opts.DelayMax = minDelay, maxDelay
		case "chunk-chars":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			minChunk, maxChunk, err := parseIntRange(value, "chunk-chars")
			if err != nil {
				return opts, err
			}
			opts.ChunkMin, opts.ChunkMax = minChunk, maxChunk
		case "seed":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			seed, err := parseInt64(value, "seed")
			if err != nil {
				return opts, err
			}
			opts.Seed = seed
			opts.SeedSet = true
		case "finish":
			if !hasValue {
				return opts, fmt.Errorf("%s requires a value.", token)
			}
			reason := normalizeFinishReason(value)
			if reason == "" {
				return opts, fmt.Errorf("unsupported finish reason %q.", value)
			}
			opts.FinishReason = reason
		case "abort":
			opts.Abort = true
		case "error":
			opts.Error = true
		default:
			return opts, fmt.Errorf("unknown option %q.", token)
		}
	}
	if err := validateCommonOptions(opts); err != nil {
		return opts, err
	}
	return opts, nil
}

func validateCommonOptions(opts commonCommandOptions) error {
	if opts.Abort && opts.Error {
		return fmt.Errorf("--abort and --error cannot be combined.")
	}
	if (opts.Abort || opts.Error) && opts.FinishReason != "stop" {
		return fmt.Errorf("--finish cannot be combined with --abort or --error.")
	}
	if opts.ChunkMin <= 0 || opts.ChunkMax < opts.ChunkMin {
		return fmt.Errorf("invalid chunk size range %d:%d.", opts.ChunkMin, opts.ChunkMax)
	}
	if opts.DelayMin < 0 || opts.DelayMax < opts.DelayMin {
		return fmt.Errorf("invalid delay range %s:%s.", opts.DelayMin, opts.DelayMax)
	}
	return nil
}

func parseToolSpec(raw string, idx int) (toolSpec, error) {
	parts := strings.Split(raw, "#")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return toolSpec{}, fmt.Errorf("tool spec %q is missing a tool name.", raw)
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
			return toolSpec{}, fmt.Errorf("unknown tool tag %q in %q.", tag, raw)
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
		return toolSpec{}, fmt.Errorf("tool spec %q has conflicting final state tags.", raw)
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

func parsePositiveInt(raw string, label string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s %q.", label, raw)
	}
	return value, nil
}

func parseNonNegativeInt(raw string, label string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s %q.", label, raw)
	}
	return value, nil
}

func parseInt64(raw string, label string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q.", label, raw)
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
		return 0, 0, fmt.Errorf("invalid %s range %q.", label, raw)
	}
	return minValue, maxValue, nil
}

func (r demoRunner) runLorem(ctx context.Context, turn *bridgesdk.Turn, cmd loremCommand, _ zerolog.Logger) error {
	started := r.runtime.now()
	opts := cmd.Options
	rng := rngForOptions(opts.SeedSet, opts.Seed, started.UnixNano())
	contentRNG := rand.New(rand.NewSource(rng.Int63()))
	stepCount := cmd.Options.Steps
	if stepCount <= 0 {
		stepCount = 1
	}
	text := buildLoremText(cmd.Chars, contentRNG)
	reasoning := buildLoremText(cmd.Options.ReasoningChars, contentRNG)
	for step := 0; step < stepCount; step++ {
		if cmd.Options.Steps > 0 {
			turn.Writer().StepStart(ctx)
		}
		r.emitCommonDecorations(ctx, turn, opts, cmd.Chars, step, stepCount)
		if reasoning != "" {
			segment := sliceByStep(reasoning, stepCount, step)
			if err := r.streamReasoning(ctx, turn, segment, rng, opts); err != nil {
				return err
			}
		}
		segment := sliceByStep(text, stepCount, step)
		if err := r.streamVisibleText(ctx, turn, segment, rng, opts); err != nil {
			return err
		}
		if cmd.Options.Steps > 0 {
			turn.Writer().StepFinish(ctx)
		}
	}
	r.finishTurn(turn, opts)
	return nil
}

func (r demoRunner) runTools(ctx context.Context, turn *bridgesdk.Turn, cmd toolsCommand, _ zerolog.Logger) error {
	started := r.runtime.now()
	opts := cmd.Options
	rng := rngForOptions(opts.SeedSet, opts.Seed, started.UnixNano())
	contentRNG := rand.New(rand.NewSource(rng.Int63()))
	phaseCount := max(len(cmd.Tools)+1, max(opts.Steps, 1))
	text := buildLoremText(cmd.Chars, contentRNG)
	reasoning := buildLoremText(cmd.Options.ReasoningChars, contentRNG)
	for phase := 0; phase < phaseCount; phase++ {
		turn.Writer().StepStart(ctx)
		r.emitCommonDecorations(ctx, turn, opts, cmd.Chars, phase, phaseCount)
		if reasoning != "" {
			if err := r.streamReasoning(ctx, turn, sliceByStep(reasoning, phaseCount, phase), rng, opts); err != nil {
				return err
			}
		}
		if err := r.streamVisibleText(ctx, turn, sliceByStep(text, phaseCount, phase), rng, opts); err != nil {
			return err
		}
		if phase < len(cmd.Tools) {
			if err := r.runToolSpec(ctx, turn, cmd.Tools[phase], rng, opts, zerolog.Nop()); err != nil {
				return err
			}
		}
		turn.Writer().StepFinish(ctx)
	}
	r.finishTurn(turn, opts)
	return nil
}

func (r demoRunner) runRandom(ctx context.Context, turn *bridgesdk.Turn, cmd randomCommand, log zerolog.Logger) error {
	started := r.runtime.now()
	seed := cmd.Seed
	if !cmd.SeedSet {
		seed = started.UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	var stepOpen bool
	for action := 0; action < cmd.Actions; action++ {
		if action > 0 {
			delay := r.sampleDelay(rng, cmd.DelayMin, cmd.DelayMax)
			if err := r.runtime.sleep(ctx, delay); err != nil {
				return err
			}
		}
		kind := chooseRandomAction(cmd, rng)
		switch kind {
		case randomActionText:
			chars := 40 + rng.Intn(160)
			text := buildLoremText(chars, rand.New(rand.NewSource(rng.Int63())))
			if err := r.streamVisibleText(ctx, turn, text, rng, commonCommandOptions{}); err != nil {
				return err
			}
		case randomActionReasoning:
			chars := 30 + rng.Intn(120)
			reasoning := buildLoremText(chars, rand.New(rand.NewSource(rng.Int63())))
			if err := r.streamReasoning(ctx, turn, reasoning, rng, commonCommandOptions{}); err != nil {
				return err
			}
		case randomActionStep:
			if !stepOpen {
				turn.Writer().StepStart(ctx)
			} else {
				turn.Writer().StepFinish(ctx)
			}
			stepOpen = !stepOpen
		case randomActionToolOK:
			if err := r.runToolSpec(ctx, turn, toolSpec{Name: randomToolName(rng), SequenceIndex: action + 1}, rng, commonCommandOptions{}, log); err != nil {
				return err
			}
		case randomActionToolFail:
			if err := r.runToolSpec(ctx, turn, toolSpec{Name: randomToolName(rng), Fail: true, SequenceIndex: action + 1}, rng, commonCommandOptions{}, log); err != nil {
				return err
			}
		case randomActionToolApprove:
			if err := r.runToolSpec(ctx, turn, toolSpec{Name: randomToolName(rng), Approval: true, SequenceIndex: action + 1}, rng, commonCommandOptions{}, log); err != nil {
				return err
			}
		case randomActionToolDeny:
			if err := r.runToolSpec(ctx, turn, toolSpec{Name: randomToolName(rng), Deny: true, SequenceIndex: action + 1}, rng, commonCommandOptions{}, log); err != nil {
				return err
			}
		case randomActionSource:
			turn.Writer().SourceURL(ctx, citations.SourceCitation{
				URL:   fmt.Sprintf("https://dummybridge.local/random/source/%d", action+1),
				Title: fmt.Sprintf("Random Source %d", action+1),
			})
		case randomActionDocument:
			turn.Writer().SourceDocument(ctx, citations.SourceDocument{
				ID:        fmt.Sprintf("random-doc-%d", action+1),
				Title:     fmt.Sprintf("Random Document %d", action+1),
				Filename:  fmt.Sprintf("random-doc-%d.txt", action+1),
				MediaType: "text/plain",
			})
		case randomActionFile:
			turn.Writer().File(ctx, fmt.Sprintf("mxc://dummybridge/random-file-%d", action+1), "application/octet-stream")
		case randomActionMetadata:
			turn.Writer().MessageMetadata(ctx, buildDemoMessageMetadata("stream-random", seed, action+1))
		case randomActionData:
			turn.Writer().Data(ctx, "random", map[string]any{"action": action + 1, "seed": seed}, false)
		case randomActionTransient:
			turn.Writer().Data(ctx, "random-transient", map[string]any{"action": action + 1}, true)
		}
	}
	switch chooseRandomTerminal(cmd, rng) {
	case "abort":
		turn.Abort("DummyBridge random mode aborted")
	case "error":
		turn.EndWithError("DummyBridge random mode failed")
	default:
		turn.End("stop")
	}
	return nil
}

func (r demoRunner) runChaos(ctx context.Context, conv *bridgesdk.Conversation, turn *bridgesdk.Turn, cmd chaosCommand, log zerolog.Logger) error {
	started := r.runtime.now()
	baseSeed := cmd.Seed
	if !cmd.SeedSet {
		baseSeed = started.UnixNano()
	}
	var wg sync.WaitGroup
	errCh := make(chan error, cmd.Turns)
	for idx := 0; idx < cmd.Turns; idx++ {
		wg.Add(1)
		childIndex := idx
		childTurn := turn
		if childIndex > 0 {
			childTurn = conv.StartTurn(ctx, dummySDKAgent(), nil)
		}
		childSeed := baseSeed + int64(childIndex+1)*97
		go func(t *bridgesdk.Turn) {
			defer wg.Done()
			childLog := log.With().Int("child_index", childIndex+1).Str("child_turn_id", t.ID()).Logger()
			staggerRNG := rand.New(rand.NewSource(childSeed + 17))
			if childIndex > 0 {
				delay := r.sampleDelay(staggerRNG, cmd.StaggerMin, cmd.StaggerMax)
				if err := r.runtime.sleep(ctx, delay); err != nil {
					t.Abort("context cancelled")
					errCh <- err
					return
				}
			}
			randomCmd := randomCommand{
				Duration:      cmd.Duration,
				Actions:       max(3, min(cmd.MaxActions, int(cmd.Duration/time.Second))),
				Profile:       cmd.Profile,
				DelayMin:      180 * time.Millisecond,
				DelayMax:      900 * time.Millisecond,
				Seed:          childSeed,
				SeedSet:       true,
				AllowAbort:    cmd.AllowAbort,
				AllowError:    cmd.AllowError,
				AllowApproval: cmd.AllowApproval,
			}
			if err := r.runRandom(ctx, t, randomCmd, childLog); err != nil {
				errCh <- err
			}
		}(childTurn)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			log.Warn().Err(err).Msg("DummyBridge chaos child failed")
			return err
		}
	}
	return nil
}

func (r demoRunner) runToolSpec(ctx context.Context, turn *bridgesdk.Turn, spec toolSpec, rng *rand.Rand, opts commonCommandOptions, _ zerolog.Logger) error {
	toolCallID := fmt.Sprintf("dummy-tool-%d-%s", spec.SequenceIndex, sanitizeToolName(spec.Name))
	input := map[string]any{
		"tool":     spec.Name,
		"sequence": spec.SequenceIndex,
		"tags":     spec.Tags,
	}
	if spec.InputError {
		turn.Writer().Tools().InputError(ctx, toolCallID, spec.Name, fmt.Sprintf("%v", input), "DummyBridge synthetic input error", spec.Provider)
	} else if spec.Delta {
		turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, nil, bridgesdk.ToolInputOptions{
			ToolName:         spec.Name,
			ProviderExecuted: spec.Provider,
			DisplayTitle:     spec.DisplayTitle,
		})
		if err := r.streamToolInput(ctx, turn, toolCallID, spec.Name, input, spec.Provider, rng, opts); err != nil {
			return err
		}
	} else {
		turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, input, bridgesdk.ToolInputOptions{
			ToolName:         spec.Name,
			ProviderExecuted: spec.Provider,
			DisplayTitle:     spec.DisplayTitle,
		})
	}
	if spec.Preliminary {
		turn.Writer().Tools().Output(ctx, toolCallID, map[string]any{
			"status": "streaming",
			"tool":   spec.Name,
		}, bridgesdk.ToolOutputOptions{ProviderExecuted: spec.Provider, Streaming: true})
	}
	if spec.Approval {
		handle := turn.Approvals().Request(bridgesdk.ApprovalRequest{
			ToolCallID: toolCallID,
			ToolName:   spec.Name,
			TTL:        10 * time.Minute,
			Presentation: &agentremote.ApprovalPromptPresentation{
				Title: spec.Name,
				Details: []agentremote.ApprovalDetail{{
					Label: "Mode",
					Value: "DummyBridge demo approval",
				}},
				AllowAlways: true,
			},
		})
		resp, err := handle.Wait(ctx)
		if err != nil {
			return err
		}
		if !resp.Approved {
			turn.Writer().Tools().Denied(ctx, toolCallID)
			return nil
		}
	}
	if spec.Deny {
		turn.Writer().Tools().Denied(ctx, toolCallID)
		return nil
	}
	if spec.Fail || spec.InputError {
		turn.Writer().Tools().OutputError(ctx, toolCallID, "DummyBridge synthetic tool failure", spec.Provider)
		return nil
	}
	turn.Writer().Tools().Output(ctx, toolCallID, map[string]any{
		"status":   "ok",
		"tool":     spec.Name,
		"sequence": spec.SequenceIndex,
	}, bridgesdk.ToolOutputOptions{ProviderExecuted: spec.Provider})
	return nil
}

func (r demoRunner) streamToolInput(ctx context.Context, turn *bridgesdk.Turn, toolCallID, toolName string, input map[string]any, providerExecuted bool, rng *rand.Rand, opts commonCommandOptions) error {
	text := fmt.Sprintf("{\"tool\":%q,\"sequence\":%d}", toolName, input["sequence"])
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().Tools().InputDelta(ctx, toolCallID, toolName, chunk, providerExecuted)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) streamVisibleText(ctx context.Context, turn *bridgesdk.Turn, text string, rng *rand.Rand, opts commonCommandOptions) error {
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().TextDelta(ctx, chunk)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) streamReasoning(ctx context.Context, turn *bridgesdk.Turn, text string, rng *rand.Rand, opts commonCommandOptions) error {
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().ReasoningDelta(ctx, chunk)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) emitCommonDecorations(ctx context.Context, turn *bridgesdk.Turn, opts commonCommandOptions, chars, step, steps int) {
	if opts.Meta {
		seed := opts.Seed
		if !opts.SeedSet {
			seed = int64(chars)
		}
		turn.Writer().MessageMetadata(ctx, buildDemoMessageMetadata("demo", seed, step+1))
	}
	for i := 0; i < splitCount(opts.Sources, steps, step); i++ {
		turn.Writer().SourceURL(ctx, citations.SourceCitation{
			URL:   fmt.Sprintf("https://dummybridge.local/source/%d-%d", step+1, i+1),
			Title: fmt.Sprintf("Demo Source %d.%d", step+1, i+1),
		})
	}
	for i := 0; i < splitCount(opts.Documents, steps, step); i++ {
		turn.Writer().SourceDocument(ctx, citations.SourceDocument{
			ID:        fmt.Sprintf("demo-doc-%d-%d", step+1, i+1),
			Title:     fmt.Sprintf("Demo Document %d.%d", step+1, i+1),
			Filename:  fmt.Sprintf("demo-doc-%d-%d.txt", step+1, i+1),
			MediaType: "text/plain",
		})
	}
	for i := 0; i < splitCount(opts.Files, steps, step); i++ {
		turn.Writer().File(ctx, fmt.Sprintf("mxc://dummybridge/demo-file-%d-%d", step+1, i+1), "application/octet-stream")
	}
	if step == 0 && strings.TrimSpace(opts.DataName) != "" {
		turn.Writer().Data(ctx, opts.DataName, map[string]any{
			"mode":  "persistent",
			"stage": step + 1,
		}, false)
	}
	if step == 0 && strings.TrimSpace(opts.DataTransientName) != "" {
		turn.Writer().Data(ctx, opts.DataTransientName, map[string]any{
			"mode":  "transient",
			"stage": step + 1,
		}, true)
	}
}

func (r demoRunner) finishTurn(turn *bridgesdk.Turn, opts commonCommandOptions) {
	switch {
	case opts.Abort:
		turn.Abort("DummyBridge synthetic abort")
	case opts.Error:
		turn.EndWithError("DummyBridge synthetic error")
	default:
		turn.End(opts.FinishReason)
	}
}

func buildDemoMessageMetadata(command string, seed int64, step int) map[string]any {
	return map[string]any{
		"command":           command,
		"seed":              seed,
		"step":              step,
		"model":             "dummybridge-demo",
		"prompt_tokens":     100 + step,
		"completion_tokens": 200 + step,
	}
}

func chooseRandomAction(cmd randomCommand, rng *rand.Rand) randomActionKind {
	type weightedAction struct {
		kind   randomActionKind
		weight int
	}
	weights := []weightedAction{
		{kind: randomActionText, weight: 6},
		{kind: randomActionReasoning, weight: 4},
		{kind: randomActionStep, weight: 2},
		{kind: randomActionToolOK, weight: 3},
		{kind: randomActionToolFail, weight: 2},
		{kind: randomActionSource, weight: 2},
		{kind: randomActionDocument, weight: 2},
		{kind: randomActionFile, weight: 2},
		{kind: randomActionMetadata, weight: 2},
		{kind: randomActionData, weight: 1},
		{kind: randomActionTransient, weight: 1},
	}
	switch cmd.Profile {
	case "tools":
		weights = append(weights, weightedAction{kind: randomActionToolDeny, weight: 3})
		for i := range weights {
			if strings.HasPrefix(string(weights[i].kind), "tool_") {
				weights[i].weight += 4
			}
		}
	case "artifacts":
		for i := range weights {
			switch weights[i].kind {
			case randomActionSource, randomActionDocument, randomActionFile, randomActionMetadata, randomActionData, randomActionTransient:
				weights[i].weight += 4
			}
		}
	case "terminals":
		for i := range weights {
			if weights[i].kind == randomActionStep {
				weights[i].weight += 4
			}
		}
	}
	if cmd.AllowApproval {
		weights = append(weights, weightedAction{kind: randomActionToolApprove, weight: 2})
	}
	total := 0
	for _, item := range weights {
		total += item.weight
	}
	target := rng.Intn(total)
	for _, item := range weights {
		target -= item.weight
		if target < 0 {
			return item.kind
		}
	}
	return randomActionText
}

func chooseRandomTerminal(cmd randomCommand, rng *rand.Rand) string {
	options := []string{"finish"}
	if cmd.AllowAbort {
		options = append(options, "abort")
	}
	if cmd.AllowError {
		options = append(options, "error")
	}
	return options[rng.Intn(len(options))]
}

func randomToolName(rng *rand.Rand) string {
	names := []string{"search", "fetch", "summarize", "calendar", "shell", "files", "preview"}
	return names[rng.Intn(len(names))]
}

func buildLoremText(chars int, rng *rand.Rand) string {
	if chars <= 0 {
		return ""
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(chars)))
	}
	var sb strings.Builder
	sb.Grow(chars + 128)
	lastIndex := -1
	for sb.Len() < chars+64 {
		index := rng.Intn(len(loremSentenceCorpus))
		if len(loremSentenceCorpus) > 1 && index == lastIndex {
			index = (index + 1 + rng.Intn(len(loremSentenceCorpus)-1)) % len(loremSentenceCorpus)
		}
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(loremSentenceCorpus[index])
		lastIndex = index
	}
	return trimLoremText(sb.String(), chars)
}

func rngForOptions(seedSet bool, seed, fallback int64) *rand.Rand {
	if !seedSet {
		seed = fallback
	}
	return rand.New(rand.NewSource(seed))
}

func chunkText(text string, rng *rand.Rand, minChunk, maxChunk int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if minChunk <= 0 {
		minChunk = defaultChunkMin
	}
	if maxChunk < minChunk {
		maxChunk = minChunk
	}
	chunks := make([]string, 0, max(1, len(text)/maxChunk+1))
	for len(text) > 0 {
		size := minChunk
		if maxChunk > minChunk {
			size += rng.Intn(maxChunk - minChunk + 1)
		}
		if size > len(text) {
			size = len(text)
		}
		chunks = append(chunks, text[:size])
		text = text[size:]
	}
	return chunks
}

func splitCount(total, parts, index int) int {
	if total <= 0 || parts <= 0 || index < 0 || index >= parts {
		return 0
	}
	base := total / parts
	remainder := total % parts
	if index < remainder {
		return base + 1
	}
	return base
}

func sliceByStep(text string, parts, index int) string {
	if parts <= 1 || text == "" {
		return text
	}
	start := 0
	for i := 0; i < index; i++ {
		start += splitCount(len(text), parts, i)
	}
	length := splitCount(len(text), parts, index)
	if start >= len(text) || length <= 0 {
		return ""
	}
	end := start + length
	if end > len(text) {
		end = len(text)
	}
	return text[start:end]
}

func (r demoRunner) sampleDelay(rng *rand.Rand, minDelay, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}
	diff := maxDelay - minDelay
	return minDelay + time.Duration(rng.Int63n(int64(diff)+1))
}

func trimLoremText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	if limit < 24 {
		return trimTrailingPunctuation(trimToWordBoundary(text[:limit]))
	}
	minCutoff := max(1, (limit*3)/4)
	for i := min(limit, len(text)); i >= minCutoff; i-- {
		switch text[i-1] {
		case '.', '!', '?':
			return strings.TrimSpace(text[:i])
		}
	}
	for i := min(limit, len(text)); i >= minCutoff; i-- {
		if text[i-1] == ' ' {
			return trimTrailingPunctuation(strings.TrimSpace(text[:i]))
		}
	}
	return trimTrailingPunctuation(strings.TrimSpace(text[:limit]))
}

func trimToWordBoundary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.LastIndexByte(text, ' '); idx > 0 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}

func trimTrailingPunctuation(text string) string {
	return strings.TrimRight(strings.TrimSpace(text), ",;:")
}

func sanitizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	if name == "" {
		return "tool"
	}
	return name
}

func snapshotParts(turn *bridgesdk.Turn) []map[string]any {
	ui := streamui.SnapshotUIMessage(turn.UIState())
	if ui == nil {
		return nil
	}
	rawParts, ok := ui["parts"].([]any)
	if !ok {
		return nil
	}
	parts := make([]map[string]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, ok := raw.(map[string]any)
		if ok {
			parts = append(parts, part)
		}
	}
	return parts
}
