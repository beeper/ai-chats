package connector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"

	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

const (
	compactionBaseChunkRatio                     = 0.4
	compactionMinChunkRatio                      = 0.15
	compactionSafetyMargin                       = 1.2
	compactionSummarizationOverhead              = 4096
	compactionDefaultSummaryFallback             = "No prior history."
	compactionDefaultSummaryParts                = 2
	compactionDefaultMinMessagesForSplit         = 4
	compactionMergeSummariesInstructions         = "Merge these partial summaries into a single cohesive summary. Preserve decisions, TODOs, open questions, and any constraints."
	compactionIdentifierPreservationInstructions = "Preserve all opaque identifiers exactly as written (no shortening or reconstruction), including UUIDs, hashes, IDs, tokens, API keys, hostnames, IPs, ports, URLs, and file names."
	compactionSummarizationSystemPrompt          = "You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified.\n\nDo NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary."
	compactionSummarizationPrompt                = "The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.\n\nUse this EXACT format:\n\n## Goal\n[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]\n\n## Constraints & Preferences\n- [Any constraints, preferences, or requirements mentioned by user]\n- [Or \"(none)\" if none were mentioned]\n\n## Progress\n### Done\n- [x] [Completed tasks/changes]\n\n### In Progress\n- [ ] [Current work]\n\n### Blocked\n- [Issues preventing progress, if any]\n\n## Key Decisions\n- **[Decision]**: [Brief rationale]\n\n## Next Steps\n1. [Ordered list of what should happen next]\n\n## Critical Context\n- [Any data, examples, or references needed to continue]\n- [Or \"(none)\" if not applicable]\n\nKeep each section concise. Preserve exact file paths, function names, and error messages."
	compactionSummarizationUpdatePrompt          = "The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.\n\nUpdate the existing structured summary with new information. RULES:\n- PRESERVE all existing information from the previous summary\n- ADD new progress, decisions, and context from the new messages\n- UPDATE the Progress section: move items from \"In Progress\" to \"Done\" when completed\n- UPDATE \"Next Steps\" based on what was accomplished\n- PRESERVE exact file paths, function names, and error messages\n- If something is no longer relevant, you may remove it\n\nUse this EXACT format:\n\n## Goal\n[Preserve existing goals, add new ones if the task expanded]\n\n## Constraints & Preferences\n- [Preserve existing, add new ones discovered]\n\n## Progress\n### Done\n- [x] [Include previously done items AND newly completed items]\n\n### In Progress\n- [ ] [Current work - update based on progress]\n\n### Blocked\n- [Current blockers - remove if resolved]\n\n## Key Decisions\n- **[Decision]**: [Brief rationale] (preserve all previous, add new)\n\n## Next Steps\n1. [Update based on current state]\n\n## Critical Context\n- [Preserve important context, add new if needed]\n\nKeep each section concise. Preserve exact file paths, function names, and error messages."
)

func normalizeCompactionSummaryParts(parts int, messageCount int) int {
	if parts <= 1 {
		return 1
	}
	if messageCount <= 0 {
		return 1
	}
	if parts > messageCount {
		return messageCount
	}
	return parts
}

func estimateCompactionMessagesTokens(messages []openai.ChatCompletionMessageParamUnion, model string) int {
	if len(messages) == 0 {
		return 0
	}
	total := 0
	for _, msg := range messages {
		total += estimateCompactionMessageTokens(msg, model)
	}
	if total <= 0 {
		return len(messages) * 3
	}
	return total
}

func estimateCompactionMessageTokens(msg openai.ChatCompletionMessageParamUnion, _ string) int {
	fallback := airuntime.EstimateMessageChars(msg) / airuntime.CharsPerTokenEstimate
	if fallback <= 0 {
		return 1
	}
	return fallback
}

func splitCompactionMessagesByTokenShare(
	messages []openai.ChatCompletionMessageParamUnion,
	model string,
	parts int,
) [][]openai.ChatCompletionMessageParamUnion {
	if len(messages) == 0 {
		return nil
	}
	normalizedParts := normalizeCompactionSummaryParts(parts, len(messages))
	if normalizedParts <= 1 {
		return [][]openai.ChatCompletionMessageParamUnion{messages}
	}
	totalTokens := estimateCompactionMessagesTokens(messages, model)
	if totalTokens <= 0 {
		return [][]openai.ChatCompletionMessageParamUnion{messages}
	}
	targetTokens := float64(totalTokens) / float64(normalizedParts)
	chunks := make([][]openai.ChatCompletionMessageParamUnion, 0, normalizedParts)
	current := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)/normalizedParts+1)
	currentTokens := 0
	for _, msg := range messages {
		msgTokens := estimateCompactionMessageTokens(msg, model)
		if len(chunks) < normalizedParts-1 && len(current) > 0 && float64(currentTokens+msgTokens) > targetTokens {
			chunks = append(chunks, current)
			current = make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)/normalizedParts+1)
			currentTokens = 0
		}
		current = append(current, msg)
		currentTokens += msgTokens
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func chunkCompactionMessagesByMaxTokens(
	messages []openai.ChatCompletionMessageParamUnion,
	model string,
	maxTokens int,
) [][]openai.ChatCompletionMessageParamUnion {
	if len(messages) == 0 {
		return nil
	}
	effectiveMax := int(math.Floor(float64(maxTokens) / compactionSafetyMargin))
	if effectiveMax <= 0 {
		effectiveMax = 1
	}

	chunks := make([][]openai.ChatCompletionMessageParamUnion, 0, 2)
	current := make([]openai.ChatCompletionMessageParamUnion, 0, 8)
	currentTokens := 0
	for _, msg := range messages {
		msgTokens := estimateCompactionMessageTokens(msg, model)
		if len(current) > 0 && currentTokens+msgTokens > effectiveMax {
			chunks = append(chunks, current)
			current = make([]openai.ChatCompletionMessageParamUnion, 0, 8)
			currentTokens = 0
		}
		current = append(current, msg)
		currentTokens += msgTokens
		if msgTokens > effectiveMax {
			chunks = append(chunks, current)
			current = make([]openai.ChatCompletionMessageParamUnion, 0, 8)
			currentTokens = 0
		}
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func computeCompactionAdaptiveChunkRatio(
	messages []openai.ChatCompletionMessageParamUnion,
	model string,
	contextWindow int,
) float64 {
	if len(messages) == 0 || contextWindow <= 0 {
		return compactionBaseChunkRatio
	}
	totalTokens := estimateCompactionMessagesTokens(messages, model)
	if totalTokens <= 0 {
		return compactionBaseChunkRatio
	}
	avgTokens := float64(totalTokens) / float64(len(messages))
	safeAvgTokens := avgTokens * compactionSafetyMargin
	avgRatio := safeAvgTokens / float64(contextWindow)
	if avgRatio > 0.1 {
		reduction := math.Min(avgRatio*2, compactionBaseChunkRatio-compactionMinChunkRatio)
		return math.Max(compactionMinChunkRatio, compactionBaseChunkRatio-reduction)
	}
	return compactionBaseChunkRatio
}

func isOversizedForCompactionSummary(
	msg openai.ChatCompletionMessageParamUnion,
	model string,
	contextWindow int,
) bool {
	if contextWindow <= 0 {
		return false
	}
	tokens := float64(estimateCompactionMessageTokens(msg, model)) * compactionSafetyMargin
	return tokens > float64(contextWindow)*0.5
}

func resolveCompactionIdentifierInstructions(policy string, custom string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "strict":
		return compactionIdentifierPreservationInstructions
	case "off":
		return ""
	case "custom":
		custom = strings.TrimSpace(custom)
		if custom != "" {
			return custom
		}
		return compactionIdentifierPreservationInstructions
	default:
		return compactionIdentifierPreservationInstructions
	}
}

func buildCompactionSummarizationInstructions(
	customInstructions string,
	identifierPolicy string,
	identifierInstructions string,
) string {
	custom := strings.TrimSpace(customInstructions)
	identifier := resolveCompactionIdentifierInstructions(identifierPolicy, identifierInstructions)
	if identifier == "" && custom == "" {
		return ""
	}
	if custom == "" {
		return identifier
	}
	if identifier == "" {
		return "Additional focus:\n" + custom
	}
	return identifier + "\n\nAdditional focus:\n" + custom
}

func serializeCompactionConversation(messages []openai.ChatCompletionMessageParamUnion) string {
	if len(messages) == 0 {
		return ""
	}
	var b strings.Builder
	for _, msg := range messages {
		switch {
		case msg.OfUser != nil:
			text := strings.TrimSpace(airuntime.ExtractUserContent(msg.OfUser.Content))
			if text != "" {
				_, _ = fmt.Fprintf(&b, "[User]: %s\n\n", text)
			}
		case msg.OfAssistant != nil:
			text := strings.TrimSpace(airuntime.ExtractAssistantContent(msg.OfAssistant.Content))
			if text != "" {
				_, _ = fmt.Fprintf(&b, "[Assistant]: %s\n\n", text)
			}
			if len(msg.OfAssistant.ToolCalls) > 0 {
				toolCalls := make([]string, 0, len(msg.OfAssistant.ToolCalls))
				for _, tc := range msg.OfAssistant.ToolCalls {
					if tc.OfFunction == nil {
						continue
					}
					name := strings.TrimSpace(tc.OfFunction.Function.Name)
					args := strings.TrimSpace(tc.OfFunction.Function.Arguments)
					if name == "" {
						continue
					}
					if args != "" {
						toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", name, args))
					} else {
						toolCalls = append(toolCalls, fmt.Sprintf("%s()", name))
					}
				}
				if len(toolCalls) > 0 {
					_, _ = fmt.Fprintf(&b, "[Assistant tool calls]: %s\n\n", strings.Join(toolCalls, "; "))
				}
			}
		case msg.OfTool != nil:
			text := strings.TrimSpace(airuntime.ExtractToolContent(msg.OfTool.Content))
			if text != "" {
				_, _ = fmt.Fprintf(&b, "[Tool result]: %s\n\n", text)
			}
		case msg.OfSystem != nil:
			text := strings.TrimSpace(airuntime.ExtractSystemContent(msg.OfSystem.Content))
			if text != "" {
				_, _ = fmt.Fprintf(&b, "[System]: %s\n\n", text)
			}
		case msg.OfDeveloper != nil:
			text := strings.TrimSpace(airuntime.ExtractDeveloperContent(msg.OfDeveloper.Content))
			if text != "" {
				_, _ = fmt.Fprintf(&b, "[Developer]: %s\n\n", text)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func extractAssistantTextFromCompletion(resp *openai.ChatCompletion) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	assistant := resp.Choices[0].Message.ToAssistantMessageParam()
	union := openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
	text, _ := airuntime.ExtractMessageContent(union)
	return strings.TrimSpace(text)
}

type generateSummaryParams struct {
	Model            string
	ReserveTokens    int
	MaxSummaryTokens int
	Messages         []openai.ChatCompletionMessageParamUnion
	PreviousSummary  string
	Instructions     string
}

func (oc *AIClient) generateSummary(
	ctx context.Context,
	params generateSummaryParams,
) (string, error) {
	if oc == nil {
		return "", errors.New("missing api client")
	}
	model := strings.TrimSpace(params.Model)
	if model == "" {
		return "", errors.New("missing summarization model")
	}
	conversation := serializeCompactionConversation(params.Messages)
	if conversation == "" {
		if strings.TrimSpace(params.PreviousSummary) != "" {
			return strings.TrimSpace(params.PreviousSummary), nil
		}
		return "", errors.New("empty compaction conversation")
	}

	maxTokens := 0
	if params.ReserveTokens > 0 {
		maxTokens = int(math.Floor(float64(params.ReserveTokens) * 0.8))
	}
	if maxTokens <= 0 && params.MaxSummaryTokens > 0 {
		maxTokens = params.MaxSummaryTokens
	}
	basePrompt := compactionSummarizationPrompt
	if strings.TrimSpace(params.PreviousSummary) != "" {
		basePrompt = compactionSummarizationUpdatePrompt
	}
	if custom := strings.TrimSpace(params.Instructions); custom != "" {
		basePrompt += "\n\nAdditional focus: " + custom
	}
	var promptText strings.Builder
	promptText.WriteString("<conversation>\n")
	promptText.WriteString(conversation)
	promptText.WriteString("\n</conversation>\n\n")
	if ps := strings.TrimSpace(params.PreviousSummary); ps != "" {
		promptText.WriteString("<previous-summary>\n")
		promptText.WriteString(ps)
		promptText.WriteString("\n</previous-summary>\n\n")
	}
	promptText.WriteString(basePrompt)

	request := openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(compactionSummarizationSystemPrompt),
			openai.UserMessage(promptText.String()),
		},
	}
	if maxTokens > 0 {
		request.MaxCompletionTokens = openai.Int(int64(maxTokens))
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := oc.api.Chat.Completions.New(ctx, request)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}
			lastErr = err
			if attempt < 2 {
				timer := time.NewTimer(time.Duration(500*(attempt+1)) * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return "", ctx.Err()
				case <-timer.C:
				}
			}
			continue
		}
		summary := extractAssistantTextFromCompletion(resp)
		if summary == "" {
			lastErr = errors.New("empty summary output")
			continue
		}
		return summary, nil
	}
	if lastErr == nil {
		lastErr = errors.New("summary generation failed")
	}
	return "", lastErr
}

type compactionSummaryParams struct {
	Model                  string
	ReserveTokens          int
	Messages               []openai.ChatCompletionMessageParamUnion
	MaxSummaryTokens       int
	MaxChunkTokens         int
	ContextWindow          int
	CustomInstructions     string
	IdentifierPolicy       string
	IdentifierInstructions string
	PreviousSummary        string
	Parts                  int
}

func (oc *AIClient) summarizeCompactionChunks(
	ctx context.Context,
	params compactionSummaryParams,
) (string, error) {
	if len(params.Messages) == 0 {
		if strings.TrimSpace(params.PreviousSummary) != "" {
			return strings.TrimSpace(params.PreviousSummary), nil
		}
		return compactionDefaultSummaryFallback, nil
	}
	chunks := chunkCompactionMessagesByMaxTokens(params.Messages, params.Model, params.MaxChunkTokens)
	if len(chunks) == 0 {
		if strings.TrimSpace(params.PreviousSummary) != "" {
			return strings.TrimSpace(params.PreviousSummary), nil
		}
		return compactionDefaultSummaryFallback, nil
	}
	instructions := buildCompactionSummarizationInstructions(
		params.CustomInstructions,
		params.IdentifierPolicy,
		params.IdentifierInstructions,
	)
	summary := strings.TrimSpace(params.PreviousSummary)
	for _, chunk := range chunks {
		next, err := oc.generateSummary(ctx, generateSummaryParams{
			Model:            params.Model,
			ReserveTokens:    params.ReserveTokens,
			MaxSummaryTokens: params.MaxSummaryTokens,
			Messages:         chunk,
			PreviousSummary:  summary,
			Instructions:     instructions,
		})
		if err != nil {
			return "", err
		}
		summary = strings.TrimSpace(next)
	}
	if summary == "" {
		return compactionDefaultSummaryFallback, nil
	}
	return summary, nil
}

func resolveCompactionSummaryModel(activeModel string, configuredModel string) string {
	active := strings.TrimSpace(activeModel)
	if active != "" {
		return active
	}
	return strings.TrimSpace(configuredModel)
}

func (oc *AIClient) summarizeCompactionWithFallback(
	ctx context.Context,
	params compactionSummaryParams,
) string {
	if len(params.Messages) == 0 {
		if strings.TrimSpace(params.PreviousSummary) != "" {
			return strings.TrimSpace(params.PreviousSummary)
		}
		return compactionDefaultSummaryFallback
	}
	if summary, err := oc.summarizeCompactionChunks(ctx, params); err == nil {
		return summary
	}

	small := make([]openai.ChatCompletionMessageParamUnion, 0, len(params.Messages))
	oversizedNotes := make([]string, 0, 2)
	for _, msg := range params.Messages {
		if isOversizedForCompactionSummary(msg, params.Model, params.ContextWindow) {
			_, role := airuntime.ExtractMessageContent(msg)
			if role == "" {
				role = "message"
			}
			tokens := estimateCompactionMessageTokens(msg, params.Model)
			oversizedNotes = append(oversizedNotes, fmt.Sprintf("[Large %s (~%dK tokens) omitted from summary]", role, int(math.Round(float64(tokens)/1000.0))))
			continue
		}
		small = append(small, msg)
	}

	if len(small) > 0 {
		next := params
		next.Messages = small
		partial, err := oc.summarizeCompactionChunks(ctx, next)
		if err == nil {
			if len(oversizedNotes) == 0 {
				return partial
			}
			return partial + "\n\n" + strings.Join(oversizedNotes, "\n")
		}
	}
	return fmt.Sprintf("Context contained %d messages (%d oversized). Summary unavailable due to size limits.", len(params.Messages), len(oversizedNotes))
}

func (oc *AIClient) summarizeCompactionInStages(
	ctx context.Context,
	params compactionSummaryParams,
) string {
	if len(params.Messages) == 0 {
		if strings.TrimSpace(params.PreviousSummary) != "" {
			return strings.TrimSpace(params.PreviousSummary)
		}
		return compactionDefaultSummaryFallback
	}
	minForSplit := compactionDefaultMinMessagesForSplit
	parts := normalizeCompactionSummaryParts(params.Parts, len(params.Messages))
	totalTokens := estimateCompactionMessagesTokens(params.Messages, params.Model)
	if parts <= 1 || len(params.Messages) < minForSplit || totalTokens <= params.MaxChunkTokens {
		return oc.summarizeCompactionWithFallback(ctx, params)
	}

	splits := splitCompactionMessagesByTokenShare(params.Messages, params.Model, parts)
	nonEmpty := make([][]openai.ChatCompletionMessageParamUnion, 0, len(splits))
	for _, s := range splits {
		if len(s) > 0 {
			nonEmpty = append(nonEmpty, s)
		}
	}
	if len(nonEmpty) <= 1 {
		return oc.summarizeCompactionWithFallback(ctx, params)
	}

	partialSummaries := make([]string, 0, len(nonEmpty))
	for _, chunk := range nonEmpty {
		next := params
		next.Messages = chunk
		next.PreviousSummary = ""
		partialSummaries = append(partialSummaries, oc.summarizeCompactionWithFallback(ctx, next))
	}
	if len(partialSummaries) == 1 {
		return partialSummaries[0]
	}

	summaryMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(partialSummaries))
	for _, summary := range partialSummaries {
		summaryMessages = append(summaryMessages, openai.UserMessage(summary))
	}
	mergeInstructions := compactionMergeSummariesInstructions
	if custom := strings.TrimSpace(params.CustomInstructions); custom != "" {
		mergeInstructions = mergeInstructions + "\n\n" + custom
	}
	next := params
	next.Messages = summaryMessages
	next.CustomInstructions = mergeInstructions
	next.PreviousSummary = ""
	return oc.summarizeCompactionWithFallback(ctx, next)
}

func compactionMessageEquivalent(a, b openai.ChatCompletionMessageParamUnion) bool {
	aText, aRole := airuntime.ExtractMessageContent(a)
	bText, bRole := airuntime.ExtractMessageContent(b)
	return strings.TrimSpace(aRole) == strings.TrimSpace(bRole) &&
		strings.TrimSpace(aText) == strings.TrimSpace(bText)
}

func selectDroppedCompactionMessages(
	original []openai.ChatCompletionMessageParamUnion,
	compacted []openai.ChatCompletionMessageParamUnion,
	droppedHint int,
) []openai.ChatCompletionMessageParamUnion {
	if len(original) == 0 {
		return nil
	}
	maxSuffix := len(original)
	if len(compacted) < maxSuffix {
		maxSuffix = len(compacted)
	}
	suffixMatch := 0
	for suffixMatch < maxSuffix {
		a := original[len(original)-1-suffixMatch]
		b := compacted[len(compacted)-1-suffixMatch]
		if !compactionMessageEquivalent(a, b) {
			break
		}
		suffixMatch++
	}
	droppedCount := len(original) - suffixMatch
	if droppedHint > droppedCount {
		droppedCount = droppedHint
	}
	if droppedCount <= 0 {
		return nil
	}
	if droppedCount > len(original) {
		droppedCount = len(original)
	}
	out := make([]openai.ChatCompletionMessageParamUnion, 0, droppedCount)
	for _, msg := range original[:droppedCount] {
		_, role := airuntime.ExtractMessageContent(msg)
		switch role {
		case "system", "developer":
			continue
		}
		text, _ := airuntime.ExtractMessageContent(msg)
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func injectSystemPromptAtFirstNonSystem(
	prompt []openai.ChatCompletionMessageParamUnion,
	text string,
) []openai.ChatCompletionMessageParamUnion {
	text = strings.TrimSpace(text)
	if len(prompt) == 0 || text == "" {
		return prompt
	}
	insertAt := 0
	for insertAt < len(prompt) {
		msg := prompt[insertAt]
		if msg.OfSystem != nil || msg.OfDeveloper != nil {
			insertAt++
			continue
		}
		break
	}
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(prompt)+1)
	out = append(out, prompt[:insertAt]...)
	out = append(out, openai.SystemMessage(text))
	out = append(out, prompt[insertAt:]...)
	return out
}

func (oc *AIClient) applyCompactionModelSummaryAndRefresh(
	ctx context.Context,
	meta *PortalMetadata,
	originalPrompt []openai.ChatCompletionMessageParamUnion,
	compactedPrompt []openai.ChatCompletionMessageParamUnion,
	decision airuntime.CompactionDecision,
	contextWindowTokens int,
) []openai.ChatCompletionMessageParamUnion {
	out := compactedPrompt
	if oc.pruningSummarizationEnabled() {
		dropped := selectDroppedCompactionMessages(originalPrompt, compactedPrompt, decision.DroppedCount)
		if len(dropped) > 0 {
			model := resolveCompactionSummaryModel(oc.effectiveModel(meta), oc.pruningSummarizationModel())
			allMessages := append([]openai.ChatCompletionMessageParamUnion{}, dropped...)
			allMessages = append(allMessages, compactedPrompt...)
			adaptive := computeCompactionAdaptiveChunkRatio(allMessages, model, contextWindowTokens)
			maxChunkTokens := int(math.Floor(float64(contextWindowTokens)*adaptive)) - compactionSummarizationOverhead
			if maxChunkTokens <= 0 {
				maxChunkTokens = 1
			}
			summary := oc.summarizeCompactionInStages(ctx, compactionSummaryParams{
				Model:                  model,
				ReserveTokens:          oc.pruningReserveTokens(),
				Messages:               dropped,
				MaxSummaryTokens:       oc.pruningMaxSummaryTokens(),
				MaxChunkTokens:         maxChunkTokens,
				ContextWindow:          contextWindowTokens,
				CustomInstructions:     oc.pruningCustomInstructions(),
				IdentifierPolicy:       oc.pruningIdentifierPolicy(),
				IdentifierInstructions: oc.pruningIdentifierInstructions(),
				PreviousSummary:        "",
				Parts:                  compactionDefaultSummaryParts,
			})
			summary = strings.TrimSpace(summary)
			if summary != "" {
				out = injectSystemPromptAtFirstNonSystem(out, "[Compaction summary of earlier context]\n"+summary)
			}
		}
	}
	if refresh := strings.TrimSpace(oc.pruningPostCompactionRefreshPrompt()); refresh != "" {
		out = injectSystemPromptAtFirstNonSystem(out, refresh)
	}
	return out
}
