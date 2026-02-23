package codex

import "time"

func nowMillis() int64 {
	return time.Now().UnixMilli()
}
