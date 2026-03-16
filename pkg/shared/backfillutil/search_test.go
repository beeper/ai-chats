package backfillutil

import (
	"testing"
	"time"
)

func TestIndexAtOrAfterZero(t *testing.T) {
	idx := IndexAtOrAfter(5, func(i int) time.Time {
		return time.Unix(int64(i*10), 0)
	}, time.Time{})
	if idx != 0 {
		t.Fatalf("expected 0, got %d", idx)
	}
}

func TestIndexAtOrAfterMiddle(t *testing.T) {
	times := []time.Time{
		time.Unix(10, 0),
		time.Unix(20, 0),
		time.Unix(30, 0),
		time.Unix(40, 0),
		time.Unix(50, 0),
	}
	idx := IndexAtOrAfter(len(times), func(i int) time.Time {
		return times[i]
	}, time.Unix(25, 0))
	if idx != 2 {
		t.Fatalf("expected 2, got %d", idx)
	}
}

func TestIndexAtOrAfterExact(t *testing.T) {
	times := []time.Time{
		time.Unix(10, 0),
		time.Unix(20, 0),
		time.Unix(30, 0),
	}
	idx := IndexAtOrAfter(len(times), func(i int) time.Time {
		return times[i]
	}, time.Unix(20, 0))
	if idx != 1 {
		t.Fatalf("expected 1, got %d", idx)
	}
}

func TestIndexAtOrAfterNoMatch(t *testing.T) {
	times := []time.Time{
		time.Unix(10, 0),
		time.Unix(20, 0),
		time.Unix(30, 0),
	}
	idx := IndexAtOrAfter(len(times), func(i int) time.Time {
		return times[i]
	}, time.Unix(40, 0))
	if idx != len(times) {
		t.Fatalf("expected %d, got %d", len(times), idx)
	}
}
