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

// Cursor points at the last item of a page, ordered descending by date, time (empty
// last), group and id. Group keeps the parts of a split together: the origin id of a
// part, the id of any other item.
type Cursor struct {
	Date  time.Time
	Time  string
	Group int64
	ID    int64
}

// IsZero reports whether the cursor is unset. Ids start at 1, so a zero id never points
// at an item.
func (c Cursor) IsZero() bool {
	return c.ID == 0
}

// String encodes the cursor for URLs.
func (c Cursor) String() string {
	return timex.FormatDate(c.Date) + "_" + c.Time + "_" + strconv.FormatInt(c.Group, 10) + "_" +
		strconv.FormatInt(c.ID, 10)
}

// ParseCursor decodes a cursor produced by Cursor.String.
func ParseCursor(s string) (Cursor, error) {
	parts := strings.Split(s, "_")
	if len(parts) != 4 {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	d, err := timex.ParseDate(parts[0])
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	group, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor: malformed: %w", errors.ErrInvalid)
	}
	return Cursor{Date: d, Time: parts[1], Group: group, ID: id}, nil
}

// Params selects a page: the items after After, zero for the first page, and at most
// PageSize of them. Make it with NewParams.
type Params struct {
	After    Cursor
	PageSize int
}

// NewParams returns the page of at most pageSize items after after, with pageSize
// clamped to a sane range: zero or less is the default size.
func NewParams(after Cursor, pageSize int) Params {
	const (
		defaultSize = 50
		maxSize     = 500
	)
	if pageSize <= 0 {
		pageSize = defaultSize
	}
	return Params{After: after, PageSize: min(pageSize, maxSize)}
}

// Cut cuts items, fetched with one extra item beyond pageSize, to pageSize and returns
// the cursor of the next page, zero on the last page. cursor returns the position of an
// item.
func Cut[T any](items []T, pageSize int, cursor func(T) Cursor) ([]T, Cursor) {
	if len(items) <= pageSize {
		return items, Cursor{}
	}
	items = items[:pageSize]
	return items, cursor(items[len(items)-1])
}
