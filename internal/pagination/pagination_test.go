package pagination_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	c := pagination.Cursor{Date: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), ID: 42}
	assert.Equal(t, "2026-10-06_42", c.String())
	got, err := pagination.ParseCursor(c.String())
	require.NoError(t, err)
	assert.Equal(t, c, got)

	for _, s := range []string{"", "2026-10-06", "bad_42", "2026-10-06_x"} {
		_, err := pagination.ParseCursor(s)
		require.ErrorIs(t, err, errors.ErrInvalid, s)
	}
}

func TestParamsPageLimit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 50, pagination.Params{}.PageLimit())
	assert.Equal(t, 10, pagination.Params{Limit: 10}.PageLimit())
	assert.Equal(t, 500, pagination.Params{Limit: 1000}.PageLimit())
}

func TestCut(t *testing.T) {
	t.Parallel()
	cursor := func(id int64) pagination.Cursor { return pagination.Cursor{ID: id} }

	items, next := pagination.Cut([]int64{3, 2, 1}, 2, cursor)
	assert.Equal(t, []int64{3, 2}, items)
	require.NotZero(t, next)
	assert.Equal(t, int64(2), next.ID)

	items, next = pagination.Cut([]int64{3, 2}, 2, cursor)
	assert.Equal(t, []int64{3, 2}, items)
	assert.Zero(t, next, "last page")
}
