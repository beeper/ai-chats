package dummybridge

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/beeper/agentremote/pkg/shared/citations"
	"github.com/beeper/agentremote/sdk"
	"github.com/rs/zerolog"
)

func (r demoRunner) runLorem(ctx context.Context, turn *sdk.Turn, cmd loremCommand, _ zerolog.Logger) error {
	started := r.runtime.now()
	opts := cmd.Options
	rng := rngForOptions(opts.SeedSet, opts.Seed, started.UnixNano())
	contentRNG := rand.New(rand.NewSource(rng.Int63()))
	stepCount := cmd.Options.Steps
	if stepCount <= 0 {
		stepCount = 1
	}
	text := buildDemoVisibleText(cmd.Chars, contentRNG)
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

func (r demoRunner) runTools(ctx context.Context, turn *sdk.Turn, cmd toolsCommand, _ zerolog.Logger) error {
	started := r.runtime.now()
	opts := cmd.Options
	rng := rngForOptions(opts.SeedSet, opts.Seed, started.UnixNano())
	contentRNG := rand.New(rand.NewSource(rng.Int63()))
	phaseCount := max(len(cmd.Tools)+1, max(opts.Steps, 1))
	text := buildDemoVisibleText(cmd.Chars, contentRNG)
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

func (r demoRunner) runRandom(ctx context.Context, turn *sdk.Turn, cmd randomCommand, log zerolog.Logger) error {
	started := r.runtime.now()
	seed := cmd.Seed
	if !cmd.SeedSet {
		seed = started.UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	var deadline time.Time
	if cmd.Duration > 0 {
		deadline = started.Add(cmd.Duration)
	}
	var stepOpen bool
	for action := 0; action < cmd.Actions; action++ {
		if !deadline.IsZero() && !r.runtime.now().Before(deadline) {
			break
		}
		if action > 0 {
			delay := r.sampleDelay(rng, cmd.DelayMin, cmd.DelayMax)
			if !deadline.IsZero() {
				remaining := deadline.Sub(r.runtime.now())
				if remaining <= 0 {
					break
				}
				if delay > remaining {
					delay = remaining
				}
			}
			if err := r.runtime.sleep(ctx, delay); err != nil {
				return err
			}
			if !deadline.IsZero() && !r.runtime.now().Before(deadline) {
				break
			}
		}
		kind := chooseRandomAction(cmd, rng)
		switch kind {
		case randomActionText:
			chars := 40 + rng.Intn(160)
			text := buildDemoVisibleText(chars, rand.New(rand.NewSource(rng.Int63())))
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

func (r demoRunner) runChaos(ctx context.Context, conv *sdk.Conversation, turn *sdk.Turn, cmd chaosCommand, log zerolog.Logger) error {
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
		go func(t *sdk.Turn) {
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
				Duration: cmd.Duration,
				Actions:  max(3, min(cmd.MaxActions, int(cmd.Duration/time.Second))),
				DelayMin: 180 * time.Millisecond,
				DelayMax: 900 * time.Millisecond,
				sharedStreamOptions: sharedStreamOptions{
					Profile:       cmd.Profile,
					Seed:          childSeed,
					SeedSet:       true,
					AllowAbort:    cmd.AllowAbort,
					AllowError:    cmd.AllowError,
					AllowApproval: cmd.AllowApproval,
				},
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

func (r demoRunner) runToolSpec(ctx context.Context, turn *sdk.Turn, spec toolSpec, rng *rand.Rand, opts commonCommandOptions, _ zerolog.Logger) error {
	toolCallID := fmt.Sprintf("dummy-tool-%d-%s", spec.SequenceIndex, sanitizeToolName(spec.Name))
	input := map[string]any{
		"tool":     spec.Name,
		"sequence": spec.SequenceIndex,
		"tags":     spec.Tags,
	}
	if spec.InputError {
		turn.Writer().Tools().InputError(ctx, toolCallID, spec.Name, fmt.Sprintf("%v", input), "DummyBridge synthetic input error", spec.Provider)
	} else if spec.Delta {
		turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, nil, sdk.ToolInputOptions{
			ToolName:         spec.Name,
			ProviderExecuted: spec.Provider,
			DisplayTitle:     spec.DisplayTitle,
		})
		if err := r.streamToolInput(ctx, turn, toolCallID, spec.Name, input, spec.Provider, rng, opts); err != nil {
			return err
		}
	} else {
		turn.Writer().Tools().EnsureInputStart(ctx, toolCallID, input, sdk.ToolInputOptions{
			ToolName:         spec.Name,
			ProviderExecuted: spec.Provider,
			DisplayTitle:     spec.DisplayTitle,
		})
	}
	if spec.Preliminary {
		turn.Writer().Tools().Output(ctx, toolCallID, map[string]any{
			"status": "streaming",
			"tool":   spec.Name,
		}, sdk.ToolOutputOptions{ProviderExecuted: spec.Provider, Streaming: true})
	}
	if spec.Approval {
		handle := turn.Approvals().Request(sdk.ApprovalRequest{
			ToolCallID: toolCallID,
			ToolName:   spec.Name,
			TTL:        10 * time.Minute,
			Presentation: &sdk.ApprovalPromptPresentation{
				Title: spec.Name,
				Details: []sdk.ApprovalDetail{{
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
	}, sdk.ToolOutputOptions{ProviderExecuted: spec.Provider})
	return nil
}

func (r demoRunner) streamToolInput(ctx context.Context, turn *sdk.Turn, toolCallID, toolName string, input map[string]any, providerExecuted bool, rng *rand.Rand, opts commonCommandOptions) error {
	text := fmt.Sprintf("{\"tool\":%q,\"sequence\":%d}", toolName, input["sequence"])
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().Tools().InputDelta(ctx, toolCallID, toolName, chunk, providerExecuted)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) streamVisibleText(ctx context.Context, turn *sdk.Turn, text string, rng *rand.Rand, opts commonCommandOptions) error {
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().TextDelta(ctx, chunk)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) streamReasoning(ctx context.Context, turn *sdk.Turn, text string, rng *rand.Rand, opts commonCommandOptions) error {
	for _, chunk := range chunkText(text, rng, opts.ChunkMin, opts.ChunkMax) {
		turn.Writer().ReasoningDelta(ctx, chunk)
		if err := r.runtime.sleep(ctx, r.sampleDelay(rng, opts.DelayMin, opts.DelayMax)); err != nil {
			return err
		}
	}
	return nil
}

func (r demoRunner) emitCommonDecorations(ctx context.Context, turn *sdk.Turn, opts commonCommandOptions, chars, step, steps int) {
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

func (r demoRunner) finishTurn(turn *sdk.Turn, opts commonCommandOptions) {
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

func (r demoRunner) sampleDelay(rng *rand.Rand, minDelay, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}
	diff := maxDelay - minDelay
	return minDelay + time.Duration(rng.Int63n(int64(diff)+1))
}
