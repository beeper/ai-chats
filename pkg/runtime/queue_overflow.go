package runtime

import "strings"

func ElideQueueText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 1 {
		return text[:1]
	}
	return strings.TrimRight(text[:limit-1], " \t\r\n") + "…"
}

func BuildQueueSummaryLine(text string, limit int) string {
	cleaned := strings.Join(strings.Fields(text), " ")
	return ElideQueueText(cleaned, limit)
}
