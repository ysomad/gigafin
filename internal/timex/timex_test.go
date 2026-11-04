package timex_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/timex"
)

func TestDate(t *testing.T) {
	t.Parallel()

	d, err := timex.ParseDate(" 2026-10-31 ")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-31", timex.FormatDate(d))
	assert.Empty(t, timex.FormatDate(time.Time{}))
	_, err = timex.ParseDate("31.10.2026")
	require.ErrorIs(t, err, timex.ErrDate)

	// 23:30 UTC on Oct 31 is already Nov 1 in Moscow
	now := timex.Fixed(time.Date(2026, 10, 31, 23, 30, 0, 0, time.UTC))
	msk := time.FixedZone("MSK", 3*60*60)
	assert.Equal(t, "2026-11-01", timex.FormatDate(timex.Today(now, msk)))
	assert.Equal(t, "2026-10-31", timex.FormatDate(timex.Today(now, time.UTC)))
}

func TestClock(t *testing.T) {
	t.Parallel()

	for give, want := range map[string]string{"9:05": "09:05", "18:48:38": "18:48", "18:48": "18:48"} {
		got, err := timex.ParseClock(give)
		require.NoError(t, err, give)
		assert.Equal(t, want, got)
	}
	_, err := timex.ParseClock("25:00")
	require.ErrorIs(t, err, timex.ErrClock)
}

func TestMonth(t *testing.T) {
	t.Parallel()

	m, err := timex.ParseMonth("2024-02")
	require.NoError(t, err)
	assert.Equal(t, "2024-02-01", timex.FormatDate(m.First()))
	assert.Equal(t, "2024-02-29", timex.FormatDate(m.Last()), "leap year")
	assert.Equal(t, "2024-01", m.Prev().String())
	assert.Equal(t, "2024-03", m.Next().String())
	assert.Equal(t, "2023-12", timex.MonthOf(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)).Prev().String())

	_, err = timex.ParseMonth("2024-13")
	require.ErrorIs(t, err, timex.ErrMonth)
}
