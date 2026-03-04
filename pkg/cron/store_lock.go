package cron

import (
	"strings"
	"sync"
)

var cronStoreLocks sync.Map

func storeLockForPath(path string) *sync.Mutex {
	key := strings.TrimSpace(path)
	if key == "" {
		key = "__cron_store__"
	}
	if val, ok := cronStoreLocks.Load(key); ok {
		return val.(*sync.Mutex)
	}
	mu := &sync.Mutex{}
	actual, _ := cronStoreLocks.LoadOrStore(key, mu)
	return actual.(*sync.Mutex)
}
