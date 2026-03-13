package backfillutil

import "time"

// NextStreamOrder computes a monotonically increasing stream order value
// derived from a timestamp. If the timestamp-based order would not exceed
// last, it returns last+1 to guarantee strict ordering.
func NextStreamOrder(last int64, ts time.Time) int64 {
	order := ts.UnixMilli() * 1000
	if order <= 0 {
		order = time.Now().UnixMilli() * 1000
	}
	if order <= last {
		order = last + 1
	}
	return order
}
