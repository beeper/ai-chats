package backfillutil

import (
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// PaginateParams controls how a slice of backfill entries is paginated.
// Cursor is only used for backward pagination (Forward == false).
type PaginateParams struct {
	Count              int
	Forward            bool
	Cursor             networkid.PaginationCursor
	AnchorMessage      *database.Message
	ForwardAnchorShift int // added to anchor index in forward mode (e.g. 1 to start after anchor)
}

// PaginateResult describes the selected window within the full entry slice.
type PaginateResult struct {
	Start, End int
	Cursor     networkid.PaginationCursor
	HasMore    bool
}

// Paginate selects a window of entries from a sorted slice using cursor/anchor-based pagination.
//
// findAnchor returns the index of the anchor message within the entries (and true), or false if
// not found. indexAtOrAfter returns the first index whose timestamp is >= the anchor time.
func Paginate(
	totalLen int,
	params PaginateParams,
	findAnchor func(*database.Message) (int, bool),
	indexAtOrAfter func(*database.Message) int,
) PaginateResult {
	count := params.Count
	if count <= 0 {
		count = totalLen
	}

	if params.Forward {
		return paginateForward(totalLen, count, params, findAnchor, indexAtOrAfter)
	}
	return paginateBackward(totalLen, count, params, findAnchor, indexAtOrAfter)
}

func paginateForward(
	totalLen, count int,
	params PaginateParams,
	findAnchor func(*database.Message) (int, bool),
	indexAtOrAfter func(*database.Message) int,
) PaginateResult {
	start := 0
	if params.AnchorMessage != nil {
		if idx, ok := findAnchor(params.AnchorMessage); ok {
			start = idx + params.ForwardAnchorShift
		} else {
			start = indexAtOrAfter(params.AnchorMessage)
		}
	}
	if start < 0 {
		start = 0
	}
	if start > totalLen {
		start = totalLen
	}
	end := totalLen
	hasMore := false
	if start+count < end {
		end = start + count
		hasMore = true
	}
	return PaginateResult{Start: start, End: end, HasMore: hasMore}
}

func paginateBackward(
	totalLen, count int,
	params PaginateParams,
	findAnchor func(*database.Message) (int, bool),
	indexAtOrAfter func(*database.Message) int,
) PaginateResult {
	end := totalLen
	if params.Cursor != "" {
		if idx, ok := ParseCursor(params.Cursor); ok && idx >= 0 && idx <= totalLen {
			end = idx
		}
	} else if params.AnchorMessage != nil {
		if idx, ok := findAnchor(params.AnchorMessage); ok {
			end = idx
		} else {
			end = indexAtOrAfter(params.AnchorMessage)
		}
	}
	if end < 0 {
		end = 0
	}
	if end > totalLen {
		end = totalLen
	}
	start := max(end-count, 0)
	hasMore := start > 0
	var cursor networkid.PaginationCursor
	if hasMore {
		cursor = FormatCursor(start)
	}
	return PaginateResult{Start: start, End: end, Cursor: cursor, HasMore: hasMore}
}
