package backfillutil

import (
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestPaginateForwardNoAnchor(t *testing.T) {
	r := Paginate(10, PaginateParams{Count: 3, Forward: true}, noAnchor, noTimeAnchor)
	if r.Start != 0 || r.End != 3 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func TestPaginateForwardFromAnchor(t *testing.T) {
	r := Paginate(10, PaginateParams{
		Count:              5,
		Forward:            true,
		AnchorMessage:      &database.Message{ID: "msg-3"},
		ForwardAnchorShift: 1,
	}, anchorAt(3), noTimeAnchor)
	if r.Start != 4 || r.End != 9 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func TestPaginateForwardNoShift(t *testing.T) {
	r := Paginate(10, PaginateParams{
		Count:         5,
		Forward:       true,
		AnchorMessage: &database.Message{ID: "msg-3"},
	}, anchorAt(3), noTimeAnchor)
	if r.Start != 3 || r.End != 8 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func TestPaginateBackwardNoCursor(t *testing.T) {
	r := Paginate(10, PaginateParams{Count: 4, Forward: false}, noAnchor, noTimeAnchor)
	if r.Start != 6 || r.End != 10 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
	if r.Cursor == "" {
		t.Fatal("expected cursor")
	}
}

func TestPaginateBackwardWithCursor(t *testing.T) {
	r := Paginate(10, PaginateParams{
		Count:   3,
		Forward: false,
		Cursor:  networkid.PaginationCursor("6"),
	}, noAnchor, noTimeAnchor)
	if r.Start != 3 || r.End != 6 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func TestPaginateBackwardExhausted(t *testing.T) {
	r := Paginate(5, PaginateParams{Count: 10, Forward: false}, noAnchor, noTimeAnchor)
	if r.Start != 0 || r.End != 5 || r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func TestPaginateForwardTimeFallback(t *testing.T) {
	anchor := &database.Message{Timestamp: time.Unix(50, 0)}
	r := Paginate(10, PaginateParams{
		Count:         3,
		Forward:       true,
		AnchorMessage: anchor,
	}, noAnchor, func(m *database.Message) int { return 5 })
	if r.Start != 5 || r.End != 8 || !r.HasMore {
		t.Fatalf("got %+v", r)
	}
}

func noAnchor(*database.Message) (int, bool) { return 0, false }
func noTimeAnchor(*database.Message) int     { return 0 }
func anchorAt(idx int) func(*database.Message) (int, bool) {
	return func(*database.Message) (int, bool) { return idx, true }
}
