package pagination_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	for _, c := range []pagination.Cursor{
		{Date: date, Time: "18:05", Group: 40, ID: 42},
		{Date: date, Group: 42, ID: 42},
	} {
		got, err := pagination.ParseCursor(c.String())
		require.NoError(t, err)
		assert.Equal(t, c, got)
	}
	assert.Equal(t, "MjAyNi0xMC0wNl8xODowNV80MF80Mg",
		pagination.Cursor{Date: date, Time: "18:05", Group: 40, ID: 42}.String())

	for _, s := range []string{"", "2026-10-06", "2026-10-06_42", "bad__42_42", "2026-10-06__x_42", "2026-10-06__42_x"} {
		_, err := pagination.ParseCursor(base64.RawURLEncoding.EncodeToString([]byte(s)))
		require.ErrorIs(t, err, errors.ErrInvalid, s)
	}
	_, err := pagination.ParseCursor("2026-10-06_18:05_40_42")
	require.ErrorIs(t, err, errors.ErrInvalid, "plain text")
}

func TestNewParams(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 50, pagination.NewParams(pagination.Cursor{}, 0).PageSize)
	assert.Equal(t, 10, pagination.NewParams(pagination.Cursor{}, 10).PageSize)
	assert.Equal(t, 500, pagination.NewParams(pagination.Cursor{}, 1000).PageSize)
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
