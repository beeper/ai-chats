package connector

import "context"

// contextValue extracts a typed value from a context. It returns the zero
// value of T when ctx is nil or the stored value does not match type T.
func contextValue[T any](ctx context.Context, key any) T {
	var zero T
	if ctx == nil {
		return zero
	}
	val, ok := ctx.Value(key).(T)
	if !ok {
		return zero
	}
	return val
}
