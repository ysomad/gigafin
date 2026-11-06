package service_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

// fakeFetcher publishes a rate of 100 plus the day of month on weekdays only.
type fakeFetcher struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeFetcher) Rates(_ context.Context, currency string, from, to time.Time) ([]domain.Rate, error) {
	f.mu.Lock()
	f.calls = append(f.calls, currency+" "+timex.FormatDate(from)+" "+timex.FormatDate(to))
	f.mu.Unlock()
	var rates []domain.Rate
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		rates = append(rates, domain.Rate{
			Source: domain.RateSourceCBR, Currency: currency, Date: d,
			Rate: decimal.NewFromInt(int64(100 + d.Day())),
		})
	}
	return rates, nil
}

func TestRates(t *testing.T) {
	t.Parallel()

	store := repotest.New(t)
	fetcher := &fakeFetcher{}
	// Sunday 2026-10-11
	clock := timex.Fixed(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC))
	svc, err := service.New(store, clock, slog.New(slog.DiscardHandler), service.Options{
		Session:      domain.SessionLifetime{Max: time.Hour, Idle: time.Hour},
		RateFetchers: map[domain.RateSource]service.RateFetcher{domain.RateSourceCBR: fetcher},
	})
	require.NoError(t, err)
	ctx := t.Context()

	userID := newUser(t, store)
	usd, err := svc.Accounts.Create(ctx, userID,
		domain.AccountInput{Name: "USD", Currency: "USD", Kind: domain.AccountCash})
	require.NoError(t, err)
	_, err = svc.Accounts.Create(ctx, userID, domain.AccountInput{Name: "RUB", Currency: "RUB", Kind: domain.AccountCash})
	require.NoError(t, err)
	_, err = svc.Operations.Create(ctx, userID, domain.OperationInput{
		Type: domain.OpAdjustment, AccountID: usd.ID, Amount: dec("10"), Date: day(t, "2026-10-05"),
	})
	require.NoError(t, err)

	n, err := svc.Rates.Sync(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "rates are off by default")
	assert.Empty(t, fetcher.calls, "no requests while rates are off")

	_, err = svc.Rates.Import(ctx, userID, strings.NewReader("date,from,to,rate\n2026-10-01,USD,RUB,90\n"))
	require.ErrorIs(t, err, errors.ErrConflict, "no manual rates while rates are off")

	err = svc.Settings.Set(ctx, userID, domain.Settings{RateSource: domain.RateSourceCBR, PrimaryCurrency: "EUR"})
	require.NoError(t, err)
	n, err = svc.Rates.Sync(ctx)
	require.NoError(t, err)
	// EUR as the primary currency and USD of the account, RUB is the pivot; 5 to 11 October
	assert.Equal(t, 14, n)
	assert.Len(t, fetcher.calls, 2)

	latest, err := svc.Rates.Latest(ctx, userID)
	require.NoError(t, err)
	require.Len(t, latest, 2)
	assert.Equal(t, "EUR", latest[0].Currency)
	assert.Equal(t, day(t, "2026-10-11"), latest[1].Date)
	assert.True(t, decimal.NewFromInt(109).Equal(latest[1].Rate), "Friday rate carried over the weekend")

	n, err = svc.Rates.Sync(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "nothing missing")
	assert.Len(t, fetcher.calls, 2, "no requests when up to date")

	// manual rate on the last day survives a sync and is reset to the source one
	require.NoError(t, svc.Rates.Set(ctx, userID, "usd", "2026-10-11", "95,5"))
	latest, err = svc.Rates.Latest(ctx, userID)
	require.NoError(t, err)
	assert.True(t, latest[1].Manual)
	assert.True(t, decimal.RequireFromString("95.5").Equal(latest[1].Rate))

	require.NoError(t, svc.Rates.Reset(ctx, userID, "USD", "2026-10-11"))
	latest, err = svc.Rates.Latest(ctx, userID)
	require.NoError(t, err)
	assert.False(t, latest[1].Manual)
	assert.True(t, decimal.NewFromInt(109).Equal(latest[1].Rate))

	// an import fills days between its rows and extends the stored range
	n, err = svc.Rates.Import(ctx, userID, strings.NewReader(
		"date,from,to,rate\n2026-09-28,USD,RUB,80\n2026-10-01,USD,RUB,81\n"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)

	err = svc.Rates.Set(ctx, userID, "RUB", "2026-10-11", "1")
	require.ErrorIs(t, err, errors.ErrInvalid, "pivot has no rate")
}
