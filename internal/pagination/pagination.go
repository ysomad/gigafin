// Package pagination pages lists by keyset: a page ends at a cursor, and the next page
// starts after it. It is not part of the domain; handlers parse cursors from requests,
// services cut pages and repositories seek past cursors.
package pagination

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/timex"
)

// Cursor points at the last item of a page, ordered by date and id descending.
type Cursor struct {
	Date time.Time
	ID   int64
}

// IsZero reports whether the cursor is unset. Ids start at 1, so a zero id never points
// at an item.
func (c Cursor) IsZero() bool {
	return c.ID == 0
}

// String encodes the cursor for URLs.
func (c Cursor) String() string {
	return timex.FormatDate(c.Date) + "_" + strconv.FormatInt(c.ID, 10)
}

// ParseCursor decodes a cursor produced by Cursor.String.
func ParseCursor(s string) (Cursor, error) {
	date, id, ok := strings.Cut(s, "_")
	if !ok {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	d, err := timex.ParseDate(date)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	return Cursor{Date: d, ID: n}, nil
}

// Params selects a page: the items after After, zero for the first page, and at most
// Limit of them.
type Params struct {
	After Cursor
	Limit int
}

// PageLimit returns Limit clamped to a sane range.
func (p Params) PageLimit() int {
	const (
		defaultLimit = 50
		maxLimit     = 500
	)
	if p.Limit <= 0 {
		return defaultLimit
	}
	return min(p.Limit, maxLimit)
}

// Cut cuts items, fetched with one extra item beyond limit, to limit and returns the
// cursor of the next page, zero on the last page. cursor returns the position of an
// item.
func Cut[T any](items []T, limit int, cursor func(T) Cursor) ([]T, Cursor) {
	if len(items) <= limit {
		return items, Cursor{}
	}
	items = items[:limit]
	return items, cursor(items[len(items)-1])
}
