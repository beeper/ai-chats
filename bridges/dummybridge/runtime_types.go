package dummybridge

import (
	"context"
	"math/rand"
	"time"
)

const (
	defaultChunkMin = 24
	defaultChunkMax = 96

	maxDemoChars           = 8192
	maxDemoReasoningChars  = 8192
	maxDemoToolSpecs       = 16
	maxDemoSteps           = 32
	maxDemoCollections     = 16
	maxDemoRandomActions   = 64
	maxDemoChaosTurns      = 16
	maxDemoChaosActions    = 64
	maxDemoDuration        = 5 * time.Minute
	maxDemoDelay           = 30 * time.Second
	maxDemoChunkChars      = 512
	maxDemoStagger         = 30 * time.Second
	maxDemoDurationSeconds = int(maxDemoDuration / time.Second)
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

var demoMarkdownLabels = []string{
	"release notes",
	"ops runbook",
	"incident log",
	"design memo",
	"qa checklist",
	"support brief",
}

var demoMarkdownURLs = []string{
	"https://dummybridge.local/docs/streaming",
	"https://dummybridge.local/docs/markdown",
	"https://dummybridge.local/runbooks/turns",
	"https://dummybridge.local/notes/demo-output",
	"https://dummybridge.local/reference/tooling",
}

var demoMarkdownEmphasis = []string{
	"high-signal",
	"operator-visible",
	"tool-safe",
	"incremental",
	"review-ready",
	"latency-sensitive",
}

var demoMarkdownListItems = []string{
	"Confirm the seeded output changes shape between runs.",
	"Surface enough formatting to stress the renderer.",
	"Keep deltas readable while chunks arrive out of phase.",
	"Preserve stable output for deterministic test fixtures.",
	"Expose links, tables, and code blocks without extra flags.",
	"Keep the generated prose plausible enough for manual inspection.",
}

var demoMarkdownQuoteCorpus = []string{
	"Streaming output should feel alive, not like the same paragraph repeated forever.",
	"Richer markdown gives the client something realistic to render while the turn is still open.",
	"Deterministic variety is more useful than perfect prose in a demo bridge.",
}

var demoMarkdownCodeSnippets = []string{
	"const preview = chunks.filter(Boolean).join(\"\");",
	"writer.textDelta(\"| status | value |\\n| --- | --- |\\n\");",
	"if (seeded) { return renderMarkdownBlocks(); }",
}

var demoMarkdownTableHeaders = [][]string{
	{"Metric", "Value", "Notes"},
	{"Phase", "Owner", "Status"},
	{"Artifact", "State", "Latency"},
}

var demoMarkdownTableRows = [][]string{
	{"stream", "warming", "steady deltas"},
	{"renderer", "active", "accepts markdown"},
	{"tool call", "complete", "output persisted"},
	{"search step", "queued", "awaiting sources"},
	{"summary", "ready", "links attached"},
	{"review", "running", "formatting checks"},
}

type demoSegmentSpec struct {
	name   string
	weight int
	minLen int
	build  func(*rand.Rand, int) string
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

type sharedStreamOptions struct {
	Profile       string
	Seed          int64
	SeedSet       bool
	AllowAbort    bool
	AllowError    bool
	AllowApproval bool
}

type randomCommand struct {
	Duration time.Duration
	Actions  int
	DelayMin time.Duration
	DelayMax time.Duration
	sharedStreamOptions
}

type chaosCommand struct {
	Turns      int
	Duration   time.Duration
	StaggerMin time.Duration
	StaggerMax time.Duration
	MaxActions int
	sharedStreamOptions
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
