package cron

import "strings"

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func ptrInt64(v int64) *int64 {
	return &v
}

func derefInt64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

//lint:ignore U1000 reserved for upcoming cron scheduling helpers
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
