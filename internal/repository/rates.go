package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
	"github.com/ysomad/gigafin/internal/timex"
)

// Rates stores exchange rates shared by all users.
type Rates struct {
	q *sqlc.Queries
}

// RateUser is what the rate sync needs to know of one user.
type RateUser struct {
	Settings domain.Settings
	// FirstDate is the date of the user's first operation, zero without operations.
	FirstDate time.Time
	// Currencies are the currencies of the user's accounts.
	Currencies []string
}

// Sync stores rates fetched from their source, keeping manual rates.
func (r *Rates) Sync(ctx context.Context, rates []domain.Rate, now time.Time) error {
	for _, rate := range rates {
		err := r.q.UpsertSyncedRate(ctx, sqlc.UpsertSyncedRateParams{
			Source:    string(rate.Source),
			Currency:  rate.Currency,
			Date:      timex.FormatDate(rate.Date),
			Rate:      rate.Rate.String(),
			CreatedAt: now.Unix(),
		})
		if err != nil {
			return fmt.Errorf("sync rate %s %s: %w", rate.Currency, timex.FormatDate(rate.Date), err)
		}
	}
	return nil
}

// Set stores rates, replacing any stored for the same days.
func (r *Rates) Set(ctx context.Context, rates []domain.Rate, now time.Time) error {
	for _, rate := range rates {
		err := r.q.UpsertRate(ctx, sqlc.UpsertRateParams{
			Source:    string(rate.Source),
			Currency:  rate.Currency,
			Date:      timex.FormatDate(rate.Date),
			Rate:      rate.Rate.String(),
			IsManual:  rate.Manual,
			CreatedAt: now.Unix(),
		})
		if err != nil {
			return fmt.Errorf("set rate %s %s: %w", rate.Currency, timex.FormatDate(rate.Date), err)
		}
	}
	return nil
}

// Bounds returns the first and last day with a stored rate of currency, a zero range
// when none is stored.
func (r *Rates) Bounds(ctx context.Context, source domain.RateSource, currency string) (domain.DateRange, error) {
	b, err := r.q.GetRateBounds(ctx, sqlc.GetRateBoundsParams{Source: string(source), Currency: currency})
	if err != nil {
		return domain.DateRange{}, fmt.Errorf("get rate bounds: %w", err)
	}
	if b.FirstDate == "" {
		return domain.DateRange{}, nil
	}
	from, err := timex.ParseDate(b.FirstDate)
	if err != nil {
		return domain.DateRange{}, fmt.Errorf("rate date %q: %w", b.FirstDate, err)
	}
	to, err := timex.ParseDate(b.LastDate)
	if err != nil {
		return domain.DateRange{}, fmt.Errorf("rate date %q: %w", b.LastDate, err)
	}
	return domain.DateRange{From: from, To: to}, nil
}

// Latest returns the last stored rate of each currency of source, by currency.
func (r *Rates) Latest(ctx context.Context, source domain.RateSource) ([]domain.Rate, error) {
	rows, err := r.q.ListLatestRates(ctx, string(source))
	if err != nil {
		return nil, fmt.Errorf("list latest rates: %w", err)
	}
	return ratesFromRows(rows)
}

func ratesFromRows(rows []sqlc.ExchangeRate) ([]domain.Rate, error) {
	rates := make([]domain.Rate, len(rows))
	for i, row := range rows {
		date, err := timex.ParseDate(row.Date)
		if err != nil {
			return nil, fmt.Errorf("rate date %q: %w", row.Date, err)
		}
		rate, err := decimal.NewFromString(row.Rate)
		if err != nil {
			return nil, fmt.Errorf("rate %q: %w", row.Rate, err)
		}
		rates[i] = domain.Rate{
			Source: domain.RateSource(row.Source), Currency: row.Currency, Date: date, Rate: rate,
			Manual: row.IsManual,
		}
	}
	return rates, nil
}

// List returns the stored rates of currency of source on days, by date.
func (r *Rates) List(
	ctx context.Context, source domain.RateSource, currency string, days domain.DateRange,
) ([]domain.Rate, error) {
	rows, err := r.q.ListRates(ctx, sqlc.ListRatesParams{
		Source: string(source), Currency: currency,
		FromDate: timex.FormatDate(days.From), ToDate: timex.FormatDate(days.To),
	})
	if err != nil {
		return nil, fmt.Errorf("list rates: %w", err)
	}
	return ratesFromRows(rows)
}

// Users returns every user with stored settings.
func (r *Rates) Users(ctx context.Context) ([]RateUser, error) {
	rows, err := r.q.ListRateUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rate users: %w", err)
	}
	users := make([]RateUser, len(rows))
	for i, row := range rows {
		u := RateUser{Settings: domain.Settings{
			RateSource: domain.RateSource(row.RateSource), PrimaryCurrency: row.PrimaryCurrency,
		}}
		if row.FirstDate != "" {
			if u.FirstDate, err = timex.ParseDate(row.FirstDate); err != nil {
				return nil, fmt.Errorf("operation date %q: %w", row.FirstDate, err)
			}
		}
		if row.Currencies != "" {
			u.Currencies = strings.Split(row.Currencies, ",")
		}
		users[i] = u
	}
	return users, nil
}
