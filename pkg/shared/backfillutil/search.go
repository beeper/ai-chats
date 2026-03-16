package backfillutil

import (
	"sort"
	"time"
)

// IndexAtOrAfter returns the first index i in [0, n) where getTime(i) >= anchor,
// using binary search. Returns 0 if anchor is zero.
func IndexAtOrAfter(n int, getTime func(i int) time.Time, anchor time.Time) int {
	if anchor.IsZero() {
		return 0
	}
	return sort.Search(n, func(i int) bool {
		return !getTime(i).Before(anchor)
	})
}
