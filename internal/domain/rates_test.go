package domain_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

func rateDay(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestFillRates(t *testing.T) {
	t.Parallel()

	rate := func(date, v string) domain.Rate {
		return domain.Rate{
			Source: domain.RateSourceCBR, Currency: "USD", Date: rateDay(date),
			Rate: decimal.RequireFromString(v),
		}
	}
	// out of order, with a gap over a weekend and one published before the range
	published := []domain.Rate{rate("2026-10-06", "3"), rate("2026-09-30", "1"), rate("2026-10-03", "2")}

	got := domain.FillRates(published, rateDay("2026-10-02"), rateDay("2026-10-07"))
	want := []string{"1", "2", "2", "2", "3", "3"}
	require.Len(t, got, len(want))
	for i, w := range want {
		assert.Equal(t, rateDay("2026-10-02").AddDate(0, 0, i), got[i].Date)
		assert.True(t, decimal.RequireFromString(w).Equal(got[i].Rate), "day %d", i)
		assert.Equal(t, "USD", got[i].Currency)
	}

	got = domain.FillRates(published, rateDay("2026-09-28"), rateDay("2026-09-30"))
	require.Len(t, got, 1, "days before the first published rate are left out")
	assert.Equal(t, rateDay("2026-09-30"), got[0].Date)

	assert.Empty(t, domain.FillRates(nil, rateDay("2026-10-01"), rateDay("2026-10-02")))
}

func TestRateGaps(t *testing.T) {
	t.Parallel()

	today := rateDay("2026-11-04")
	tests := []struct {
		name   string
		first  time.Time
		stored domain.DateRange
		want   []domain.DateRange
	}{
		{
			"none stored", rateDay("2026-01-01"),
			domain.DateRange{},
			[]domain.DateRange{{From: rateDay("2026-01-01"), To: today}},
		},
		{"up to date", rateDay("2026-01-01"), domain.DateRange{From: rateDay("2026-01-01"), To: today}, nil},
		{
			"older operations", rateDay("2025-12-01"),
			domain.DateRange{From: rateDay("2026-01-01"), To: today},
			[]domain.DateRange{{From: rateDay("2025-12-01"), To: rateDay("2025-12-31")}},
		},
		{
			"behind, refetches the last week", rateDay("2026-01-01"),
			domain.DateRange{From: rateDay("2026-01-01"), To: rateDay("2026-11-01")},
			[]domain.DateRange{{From: rateDay("2026-10-25"), To: today}},
		},
		{
			"refetch stays in stored", rateDay("2026-11-01"),
			domain.DateRange{From: rateDay("2026-11-01"), To: rateDay("2026-11-02")},
			[]domain.DateRange{{From: rateDay("2026-11-01"), To: today}},
		},
		{"first in the future", rateDay("2026-12-01"), domain.DateRange{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, domain.RateGaps(tt.first, today, tt.stored))
		})
	}
}

func TestNewRate(t *testing.T) {
	t.Parallel()

	r, err := domain.NewRate(domain.RateSourceCBR, "USD", "RUB", rateDay("2026-10-01"), decimal.NewFromInt(80))
	require.NoError(t, err)
	assert.Equal(t, "USD", r.Currency)
	assert.True(t, decimal.NewFromInt(80).Equal(r.Rate))

	r, err = domain.NewRate(domain.RateSourceCBR, "RUB", "USD", rateDay("2026-10-01"), decimal.RequireFromString("0.0125"))
	require.NoError(t, err)
	assert.Equal(t, "USD", r.Currency, "inverted")
	assert.True(t, decimal.NewFromInt(80).Equal(r.Rate))

	for _, tt := range []struct {
		src      domain.RateSource
		from, to string
		rate     string
	}{
		{domain.RateSourceNone, "USD", "RUB", "80"},
		{domain.RateSourceCBR, "USD", "EUR", "1"},
		{domain.RateSourceCBR, "RUB", "RUB", "1"},
		{domain.RateSourceCBR, "XXX", "RUB", "1"},
		{domain.RateSourceCBR, "USD", "RUB", "-1"},
	} {
		_, err := domain.NewRate(tt.src, tt.from, tt.to, rateDay("2026-10-01"), decimal.RequireFromString(tt.rate))
		require.ErrorIs(t, err, errors.ErrInvalid, "%+v", tt)
	}
}

func TestSettingsValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.Settings{}.Validate(), "rates off by default")
	require.NoError(t, domain.Settings{RateSource: domain.RateSourceCBR, PrimaryCurrency: "RUB"}.Validate())
	require.ErrorIs(t, domain.Settings{RateSource: domain.RateSourceCBR}.Validate(), errors.ErrInvalid)
	require.ErrorIs(t, domain.Settings{RateSource: "ecb", PrimaryCurrency: "RUB"}.Validate(), errors.ErrInvalid)
}

func TestRateCurrencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		src        domain.RateSource
		primary    string
		currencies []string
		want       []string
	}{
		{name: "rates off", src: domain.RateSourceNone, primary: "USD", currencies: []string{"EUR"}},
		{name: "only pivot", src: domain.RateSourceCBR, primary: "RUB", currencies: []string{"RUB"}},
		{name: "no accounts", src: domain.RateSourceCBR, primary: "RUB"},
		{
			name: "distinct sorted without pivot", src: domain.RateSourceCBR, primary: "USD",
			currencies: []string{"RUB", "EUR", "USD", "EUR"}, want: []string{"EUR", "USD"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, domain.RateCurrencies(tt.src, tt.primary, tt.currencies))
		})
	}
}
