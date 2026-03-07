package runtime

import "strings"

var abortTriggers = map[string]struct{}{
	"stop":                    {},
	"esc":                     {},
	"abort":                   {},
	"wait":                    {},
	"exit":                    {},
	"interrupt":               {},
	"halt":                    {},
	"stopp":                   {},
	"anhalten":                {},
	"aufhören":                {},
	"hoer auf":                {},
	"pare":                    {},
	"arrete":                  {},
	"arrête":                  {},
	"detente":                 {},
	"deten":                   {},
	"detén":                   {},
	"停止":                      {},
	"やめて":                     {},
	"止めて":                     {},
	"रुको":                    {},
	"توقف":                    {},
	"стоп":                    {},
	"остановись":              {},
	"останови":                {},
	"остановить":              {},
	"прекрати":                {},
	"stop openclaw":           {},
	"openclaw stop":           {},
	"stop action":             {},
	"stop current action":     {},
	"stop run":                {},
	"stop current run":        {},
	"stop agent":              {},
	"stop the agent":          {},
	"please stop":             {},
	"stop please":             {},
	"stop don't do anything":  {},
	"stop dont do anything":   {},
	"stop do not do anything": {},
	"stop doing anything":     {},
	"do not do that":          {},
}

func normalizeAbortTriggerText(text string) string {
	cleaned := strings.TrimSpace(strings.ToLower(text))
	cleaned = strings.ReplaceAll(cleaned, "’", "'")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.TrimRight(cleaned, " \t\r\n.!?…,，。;；:：'\"”’)]}")
	return strings.TrimSpace(cleaned)
}

func IsAbortTriggerText(text string) bool {
	normalized := normalizeAbortTriggerText(text)
	if normalized == "" {
		return false
	}
	_, ok := abortTriggers[normalized]
	return ok
}
