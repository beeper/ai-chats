package ai

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	mediaPlaceholderRe      = regexp.MustCompile(`^<media:[^>]+>(\s*\([^)]*\))?$`)
	mediaPlaceholderTokenRe = regexp.MustCompile(`^<media:[^>]+>(\s*\([^)]*\))?\s*`)
)

func extractMediaUserText(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	if mediaPlaceholderRe.MatchString(trimmed) {
		return ""
	}
	cleaned := strings.TrimSpace(mediaPlaceholderTokenRe.ReplaceAllString(trimmed, ""))
	return cleaned
}

func formatMediaSection(title, kind, text, userText string) string {
	lines := []string{"[" + title + "]"}
	if userText != "" {
		lines = append(lines, "User text:\n"+userText)
	}
	lines = append(lines, kind+":\n"+text)
	return strings.Join(lines, "\n")
}

func formatMediaUnderstandingBody(body string, outputs []MediaUnderstandingOutput) string {
	filtered := make([]MediaUnderstandingOutput, 0, len(outputs))
	for _, output := range outputs {
		if strings.TrimSpace(output.Text) == "" {
			continue
		}
		filtered = append(filtered, output)
	}
	if len(filtered) == 0 {
		return strings.TrimSpace(body)
	}

	userText := extractMediaUserText(body)
	var sections []string
	if userText != "" && len(filtered) > 1 {
		sections = append(sections, "User text:\n"+userText)
	}

	counts := map[MediaUnderstandingKind]int{}
	for _, output := range filtered {
		counts[output.Kind]++
	}
	seen := map[MediaUnderstandingKind]int{}

	for _, output := range filtered {
		count := counts[output.Kind]
		seen[output.Kind]++
		suffix := ""
		if count > 1 {
			suffix = " " + strconv.Itoa(seen[output.Kind]) + "/" + strconv.Itoa(count)
		}
		title, kind := mediaKindTitleAndLabel(output.Kind)
		if title != "" {
			sections = append(sections, formatMediaSection(
				title+suffix,
				kind,
				output.Text,
				userTextIfSingle(userText, len(filtered)),
			))
		}
	}

	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func mediaKindTitleAndLabel(kind MediaUnderstandingKind) (string, string) {
	switch kind {
	case MediaKindAudioTranscription:
		return "Audio", "Transcript"
	case MediaKindImageDescription:
		return "Image", "Description"
	case MediaKindVideoDescription:
		return "Video", "Description"
	default:
		return "", ""
	}
}

func userTextIfSingle(userText string, count int) string {
	if count == 1 {
		return userText
	}
	return ""
}

func formatAudioTranscripts(outputs []MediaUnderstandingOutput) string {
	if len(outputs) == 1 {
		return outputs[0].Text
	}
	parts := make([]string, 0, len(outputs))
	for idx, output := range outputs {
		parts = append(parts, "Audio "+strconv.Itoa(idx+1)+":\n"+output.Text)
	}
	return strings.Join(parts, "\n\n")
}
