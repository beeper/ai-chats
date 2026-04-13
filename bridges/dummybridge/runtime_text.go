package dummybridge

import (
	"fmt"
	"math/rand"
	"strings"
)

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

func buildDemoVisibleText(chars int, rng *rand.Rand) string {
	if chars <= 0 {
		return ""
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(int64(chars)))
	}
	segments := demoVisibleSegmentSpecs()
	var blocks []string
	total := 0
	target := chars + min(96, max(24, chars/6))
	for total < chars {
		remaining := target - total
		block := chooseDemoSegment(segments, rng, max(remaining, 0))
		if strings.TrimSpace(block) == "" {
			block = buildLoremText(min(max(chars-total, 48), 160), rand.New(rand.NewSource(rng.Int63())))
		}
		blocks = append(blocks, block)
		total += len(block)
	}
	return trimDemoVisibleText(strings.Join(blocks, "\n\n"), chars)
}

func demoVisibleSegmentSpecs() []demoSegmentSpec {
	return []demoSegmentSpec{
		{
			name:   "paragraph",
			weight: 5,
			minLen: 48,
			build: func(rng *rand.Rand, remaining int) string {
				size := 72 + rng.Intn(96)
				if remaining > 0 {
					size = min(size, remaining+48)
				}
				return buildLoremText(max(size, 48), rand.New(rand.NewSource(rng.Int63())))
			},
		},
		{
			name:   "link-paragraph",
			weight: 4,
			minLen: 96,
			build: func(rng *rand.Rand, _ int) string {
				label := demoMarkdownLabels[rng.Intn(len(demoMarkdownLabels))]
				url := demoMarkdownURLs[rng.Intn(len(demoMarkdownURLs))]
				emphasis := demoMarkdownEmphasis[rng.Intn(len(demoMarkdownEmphasis))]
				prefix := buildLoremText(72+rng.Intn(48), rand.New(rand.NewSource(rng.Int63())))
				return fmt.Sprintf("%s Review the [%s](%s) entry for **%s** output and _staged_ formatting transitions.", prefix, label, url, emphasis)
			},
		},
		{
			name:   "list",
			weight: 3,
			minLen: 96,
			build: func(rng *rand.Rand, _ int) string {
				count := 2 + rng.Intn(3)
				var lines []string
				for i := 0; i < count; i++ {
					item := demoMarkdownListItems[(rng.Intn(len(demoMarkdownListItems))+i)%len(demoMarkdownListItems)]
					prefix := "-"
					if rng.Intn(4) == 0 {
						prefix = "- [x]"
					}
					lines = append(lines, fmt.Sprintf("%s %s", prefix, item))
				}
				return strings.Join(lines, "\n")
			},
		},
		{
			name:   "quote",
			weight: 2,
			minLen: 72,
			build: func(rng *rand.Rand, _ int) string {
				quote := demoMarkdownQuoteCorpus[rng.Intn(len(demoMarkdownQuoteCorpus))]
				return fmt.Sprintf("> %s\n>\n> %s", quote, buildLoremText(48+rng.Intn(36), rand.New(rand.NewSource(rng.Int63()))))
			},
		},
		{
			name:   "code",
			weight: 2,
			minLen: 72,
			build: func(rng *rand.Rand, _ int) string {
				snippet := demoMarkdownCodeSnippets[rng.Intn(len(demoMarkdownCodeSnippets))]
				return fmt.Sprintf("Use `%s` when the client needs a smaller incremental patch.\n\n```js\n%s\n```", sanitizeToolName(demoMarkdownLabels[rng.Intn(len(demoMarkdownLabels))]), snippet)
			},
		},
		{
			name:   "table",
			weight: 2,
			minLen: 180,
			build: func(rng *rand.Rand, _ int) string {
				header := demoMarkdownTableHeaders[rng.Intn(len(demoMarkdownTableHeaders))]
				rowCount := 2 + rng.Intn(2)
				var lines []string
				lines = append(lines, fmt.Sprintf("| %s |", strings.Join(header, " | ")))
				lines = append(lines, fmt.Sprintf("| %s |", strings.Join([]string{"---", "---", "---"}, " | ")))
				for i := 0; i < rowCount; i++ {
					row := demoMarkdownTableRows[(rng.Intn(len(demoMarkdownTableRows))+i)%len(demoMarkdownTableRows)]
					lines = append(lines, fmt.Sprintf("| %s |", strings.Join(row, " | ")))
				}
				return strings.Join(lines, "\n")
			},
		},
	}
}

func chooseDemoSegment(specs []demoSegmentSpec, rng *rand.Rand, remaining int) string {
	candidates := make([]demoSegmentSpec, 0, len(specs))
	totalWeight := 0
	for _, spec := range specs {
		if remaining > 0 && remaining < spec.minLen/2 {
			continue
		}
		candidates = append(candidates, spec)
		totalWeight += spec.weight
	}
	if len(candidates) == 0 {
		candidates = specs
		for _, spec := range candidates {
			totalWeight += spec.weight
		}
	}
	target := rng.Intn(totalWeight)
	for _, spec := range candidates {
		target -= spec.weight
		if target < 0 {
			return spec.build(rng, remaining)
		}
	}
	return candidates[0].build(rng, remaining)
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

func trimDemoVisibleText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	blocks := strings.Split(text, "\n\n")
	if len(blocks) > 1 {
		var kept []string
		total := 0
		for _, block := range blocks {
			block = strings.TrimSpace(block)
			if block == "" {
				continue
			}
			nextLen := total + len(block)
			if len(kept) > 0 {
				nextLen += 2
			}
			if nextLen > limit {
				break
			}
			kept = append(kept, block)
			total = nextLen
		}
		if len(kept) > 0 {
			return strings.Join(kept, "\n\n")
		}
	}
	return trimLoremText(text, limit)
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
