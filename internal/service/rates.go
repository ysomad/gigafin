package service

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// RateFetcher fetches the rates its source set for currency in the range from to to, both
// dates included. Days without a set rate have none.
type RateFetcher interface {
	Rates(ctx context.Context, currency string, from, to time.Time) ([]domain.Rate, error)
}

// Rates keeps exchange rates shared by all users up to date and lets users enter their
// own.
type Rates struct {
	store    *repository.Store
	clock    timex.Clock
	fetchers map[domain.RateSource]RateFetcher

	// mu runs one sync at a time, from the scheduler or a user
	mu sync.Mutex
}

// Sync fetches the missing rates of every currency users with rates on need, from the
// first operation of those users to today, and returns how many daily rates it stored.
// It does nothing while every user has rates off. A currency that fails does not stop
// the others; their errors are joined.
func (s *Rates) Sync(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	users, err := s.store.Read().Rates.Users(ctx)
	if err != nil {
		return 0, err
	}
	today := timex.Date(s.clock.Now().UTC())

	type key struct {
		source   domain.RateSource
		currency string
	}
	// first day each currency of a source is needed from
	first := make(map[key]time.Time)
	for _, u := range users {
		src := u.Settings.RateSource
		if src == domain.RateSourceNone {
			continue
		}
		start := cmp.Or(u.FirstDate, today)
		for _, c := range domain.RateCurrencies(src, u.Settings.PrimaryCurrency, u.Currencies) {
			k := key{source: src, currency: c}
			if f, ok := first[k]; !ok || start.Before(f) {
				first[k] = start
			}
		}
	}

	keys := slices.SortedFunc(maps.Keys(first), func(a, b key) int {
		return cmp.Or(cmp.Compare(a.source, b.source), cmp.Compare(a.currency, b.currency))
	})
	var (
		stored int
		errs   []error
	)
	for _, k := range keys {
		fetcher, ok := s.fetchers[k.source]
		if !ok {
			errs = append(errs, fmt.Errorf("rate source %q: no fetcher", k.source))
			continue
		}
		bounds, err := s.store.Read().Rates.Bounds(ctx, k.source, k.currency)
		if err != nil {
			return stored, err
		}
		for _, gap := range domain.RateGaps(first[k], today, bounds) {
			published, err := fetcher.Rates(ctx, k.currency, gap.From.Add(-domain.RateLookback), gap.To)
			if ctx.Err() != nil {
				return stored, ctx.Err()
			}
			if err != nil {
				errs = append(errs, err)
				break
			}
			rates := domain.FillRates(published, gap.From, gap.To)
			if err := s.store.InTx(ctx, func(r repository.Repos) error {
				return r.Rates.Sync(ctx, rates, s.clock.Now())
			}); err != nil {
				return stored, err
			}
			stored += len(rates)
		}
	}
	return stored, errors.Join(errs...)
}

// Latest returns the last stored rate of each currency of the user's rate source, none
// when the user has rates off.
func (s *Rates) Latest(ctx context.Context, userID int64) ([]domain.Rate, error) {
	settings, err := s.store.Read().Settings.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if settings.RateSource == domain.RateSourceNone {
		return nil, nil
	}
	return s.store.Read().Rates.Latest(ctx, settings.RateSource)
}

// Currencies returns the currencies the user needs rates of, none when the user has
// rates off or every account and the primary currency are in the pivot currency.
func (s *Rates) Currencies(ctx context.Context, userID int64) ([]string, error) {
	settings, err := s.store.Read().Settings.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	accounts, err := s.store.Read().Accounts.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	currencies := make([]string, len(accounts))
	for i, a := range accounts {
		currencies[i] = a.Currency
	}
	return domain.RateCurrencies(settings.RateSource, settings.PrimaryCurrency, currencies), nil
}

// Set stores the rate of currency on date entered by the user, priced in the pivot
// currency of the user's rate source. A sync never overwrites it.
func (s *Rates) Set(ctx context.Context, userID int64, currency, date, rate string) error {
	src, err := s.source(ctx, userID)
	if err != nil {
		return err
	}
	day, err := timex.ParseDate(date)
	if err != nil {
		return fmt.Errorf("date: %w", err)
	}
	v, err := decimal.NewFromString(strings.ReplaceAll(strings.TrimSpace(rate), ",", "."))
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "rate: %q is not a number", rate)
	}
	r, err := domain.NewRate(src, strings.ToUpper(strings.TrimSpace(currency)), src.Pivot(), day, v)
	if err != nil {
		return err
	}
	r.Manual = true
	return s.store.Write().Rates.Set(ctx, []domain.Rate{r}, s.clock.Now())
}

// Reset replaces the rate of currency on date with the one of the user's rate source,
// undoing a rate the user entered or imported.
func (s *Rates) Reset(ctx context.Context, userID int64, currency, date string) error {
	src, err := s.source(ctx, userID)
	if err != nil {
		return err
	}
	day, err := timex.ParseDate(date)
	if err != nil {
		return fmt.Errorf("date: %w", err)
	}
	fetcher, ok := s.fetchers[src]
	if !ok {
		return fmt.Errorf("rate source %q: no fetcher", src)
	}
	published, err := fetcher.Rates(ctx, currency, day.Add(-domain.RateLookback), day)
	if err != nil {
		return err
	}
	rates := domain.FillRates(published, day, day)
	if len(rates) == 0 {
		return fmt.Errorf("rate of %s on %s: %w", currency, date, errors.ErrNotFound)
	}
	return s.store.Write().Rates.Set(ctx, rates, s.clock.Now())
}

// Preview returns the daily rates Import would store from a CSV file, each with the
// rate stored for its day, by currency and date.
func (s *Rates) Preview(ctx context.Context, userID int64, r io.Reader) ([]domain.RateChange, error) {
	src, rates, err := s.parse(ctx, userID, r)
	if err != nil {
		return nil, err
	}
	changes := make([]domain.RateChange, 0, len(rates))
	// rates come by currency and date, so each currency is one run of days
	for i := 0; i < len(rates); {
		j := i
		for j < len(rates) && rates[j].Currency == rates[i].Currency {
			j++
		}
		group := rates[i:j]
		stored, err := s.store.Read().Rates.List(ctx, src, group[0].Currency,
			domain.DateRange{From: group[0].Date, To: group[len(group)-1].Date})
		if err != nil {
			return nil, err
		}
		byDay := make(map[time.Time]domain.Rate, len(stored))
		for _, old := range stored {
			byDay[old.Date] = old
		}
		for _, rate := range group {
			c := domain.RateChange{Rate: rate}
			if old, ok := byDay[rate.Date]; ok {
				c.Old = &old
			}
			changes = append(changes, c)
		}
		i = j
	}
	return changes, nil
}

// Import stores the rates of a CSV file in the format of importer.ParseRates as if the
// user entered them, carrying each rate over the following days without one up to the
// last day of its currency in the file. It returns how many daily rates it stored.
func (s *Rates) Import(ctx context.Context, userID int64, r io.Reader) (int, error) {
	_, rates, err := s.parse(ctx, userID, r)
	if err != nil {
		return 0, err
	}
	if err := s.store.InTx(ctx, func(r repository.Repos) error {
		return r.Rates.Set(ctx, rates, s.clock.Now())
	}); err != nil {
		return 0, err
	}
	return len(rates), nil
}

// parse reads a CSV file of rates of the user's rate source and returns the daily
// rates it makes, manual, by currency and date.
func (s *Rates) parse(ctx context.Context, userID int64, r io.Reader) (domain.RateSource, []domain.Rate, error) {
	src, err := s.source(ctx, userID)
	if err != nil {
		return "", nil, err
	}
	parsed, err := importer.ParseRates(src, r)
	if err != nil {
		return "", nil, err
	}
	byCurrency := make(map[string][]domain.Rate)
	for _, rate := range parsed {
		rate.Manual = true
		byCurrency[rate.Currency] = append(byCurrency[rate.Currency], rate)
	}
	var rates []domain.Rate
	for _, currency := range slices.Sorted(maps.Keys(byCurrency)) {
		published := byCurrency[currency]
		from := slices.MinFunc(published, func(a, b domain.Rate) int { return a.Date.Compare(b.Date) }).Date
		to := slices.MaxFunc(published, func(a, b domain.Rate) int { return a.Date.Compare(b.Date) }).Date
		rates = append(rates, domain.FillRates(published, from, to)...)
	}
	return src, rates, nil
}

// source returns the user's rate source; it fails with errors.ErrConflict when the user
// has rates off.
func (s *Rates) source(ctx context.Context, userID int64) (domain.RateSource, error) {
	settings, err := s.store.Read().Settings.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	if settings.RateSource == domain.RateSourceNone {
		return "", domain.NewError(errors.ErrConflict, "exchange rates are off")
	}
	return settings.RateSource, nil
}
