package dummybridge

import (
	"errors"
	"fmt"
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
