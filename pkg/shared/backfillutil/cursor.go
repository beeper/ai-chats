package backfillutil

import (
	"strconv"

	"maunium.net/go/mautrix/bridgev2/networkid"
)

func ParseCursor(cursor networkid.PaginationCursor) (int, bool) {
	if cursor == "" {
		return 0, false
	}
	idx, err := strconv.Atoi(string(cursor))
	if err != nil {
		return 0, false
	}
	return idx, true
}

func FormatCursor(idx int) networkid.PaginationCursor {
	return networkid.PaginationCursor(strconv.Itoa(idx))
}
