package domain

import (
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// RateSource is where exchange rates come from. The zero value turns rates off.
type RateSource string

const (
	// RateSourceNone turns exchange rates off.
	RateSourceNone RateSource = ""
	// RateSourceCBR is the Central Bank of Russia, rates in rubles.
	RateSourceCBR RateSource = "cbr"
)

// RateSources returns all rate sources, RateSourceNone first.
func RateSources() []RateSource {
	return []RateSource{RateSourceNone, RateSourceCBR}
}

// ParseRateSource validates a rate source string.
func ParseRateSource(s string) (RateSource, error) {
	src := RateSource(strings.TrimSpace(s))
	if !slices.Contains(RateSources(), src) {
		return "", NewError(errors.ErrInvalid, "rate source: unknown %q", s)
	}
	return src, nil
}

// Pivot returns the currency rates of s are quoted in, "" for RateSourceNone.
func (s RateSource) Pivot() string {
	if s == RateSourceCBR {
		return "RUB"
	}
	return ""
}

// RateCurrencies returns the sorted distinct currencies a user with rate source src,
// primary currency primary and accounts in currencies needs rates of: all but the pivot.
// It returns nil for RateSourceNone.
func RateCurrencies(src RateSource, primary string, currencies []string) []string {
	if src == RateSourceNone {
		return nil
	}
	var need []string
	for _, c := range append(slices.Clone(currencies), primary) {
		if c != "" && c != src.Pivot() && !slices.Contains(need, c) {
			need = append(need, c)
		}
	}
	slices.Sort(need)
	return need
}

// RateLookback is how far before a wanted day a sync asks a source for rates, so the
// first wanted day has a published rate to carry: CBR publishes none on public holidays,
// up to 12 days in a row around New Year.
const RateLookback = 31 * 24 * time.Hour

// _rateDecimals is the precision of rates stored after inverting one.
const _rateDecimals = 10

// Rate is the price of one unit of Currency in the pivot currency of Source on Date.
type Rate struct {
	Source   RateSource
	Currency string
	// Date is a timex date.
	Date time.Time
	Rate decimal.Decimal
	// Manual marks a rate entered or imported by a user; a sync never overwrites it.
	Manual bool
}

// NewRate returns a rate of source with the price of one unit of from in to, inverting
// it when from is the pivot of source. Errors are of kind errors.ErrInvalid.
func NewRate(source RateSource, from, to string, date time.Time, rate decimal.Decimal) (Rate, error) {
	pivot := source.Pivot()
	if pivot == "" {
		return Rate{}, NewError(errors.ErrInvalid, "rate source: required")
	}
	if !rate.IsPositive() {
		return Rate{}, NewError(errors.ErrInvalid, "rate: %s must be positive", rate)
	}
	if from == pivot && to != pivot {
		from, rate = to, decimal.NewFromInt(1).DivRound(rate, _rateDecimals)
		if rate.IsZero() {
			return Rate{}, NewError(errors.ErrInvalid, "rate: too large to invert")
		}
	} else if to != pivot {
		return Rate{}, NewError(errors.ErrInvalid, "rate: %s/%s: one side must be %s", from, to, pivot)
	}
	if from == pivot {
		return Rate{}, NewError(errors.ErrInvalid, "rate: %s/%s: same currency", from, to)
	}
	if !money.Valid(from) {
		return Rate{}, NewError(errors.ErrInvalid, "rate: unknown currency %q", from)
	}
	return Rate{Source: source, Currency: from, Date: date, Rate: rate}, nil
}

// FillRates returns one rate per day of the range from to to, both included, carrying the last
// published rate over days without one, as a bank applies a rate until the next is set.
// published may come in any order and start before from; days before the first of them
// are left out.
func FillRates(published []Rate, from, to time.Time) []Rate {
	published = slices.Clone(published)
	slices.SortFunc(published, func(a, b Rate) int { return a.Date.Compare(b.Date) })

	var (
		filled []Rate
		last   *Rate
		i      int
	)
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		for ; i < len(published) && !published[i].Date.After(day); i++ {
			last = &published[i]
		}
		if last == nil {
			continue
		}
		r := *last
		r.Date = day
		filled = append(filled, r)
	}
	return filled
}

// DateRange is the days from From to To, both included.
type DateRange struct {
	From time.Time
	To   time.Time
}

// RateRefresh is how many days before the last stored rate a sync fetches again, so a
// rate carried before the source published the real one gets corrected.
const RateRefresh = 7

// RateGaps returns the ranges of days a sync of one currency has to fetch: days from
// first to today before the stored ones from stored.From to stored.To, and days after
// them up to today together with the last RateRefresh stored days. A zero stored means
// none are stored. It returns nil when first is after today or every day up to today is
// stored.
func RateGaps(first, today time.Time, stored DateRange) []DateRange {
	if first.After(today) {
		return nil
	}
	if stored.From.IsZero() {
		return []DateRange{{From: first, To: today}}
	}
	var gaps []DateRange
	if first.Before(stored.From) {
		gaps = append(gaps, DateRange{From: first, To: stored.From.AddDate(0, 0, -1)})
	}
	if stored.To.Before(today) {
		from := stored.To.AddDate(0, 0, -RateRefresh)
		if from.Before(stored.From) {
			from = stored.From
		}
		gaps = append(gaps, DateRange{From: from, To: today})
	}
	return gaps
}

// RateChange is a rate about to be stored next to the one stored for its day.
type RateChange struct {
	Rate Rate
	// Old is the stored rate of the day, nil when there is none.
	Old *Rate
}

// Status returns "new" without a stored rate, "unchanged" when the stored one is equal
// and "changed" otherwise.
func (c RateChange) Status() string {
	switch {
	case c.Old == nil:
		return "new"
	case c.Old.Rate.Equal(c.Rate.Rate):
		return "unchanged"
	default:
		return "changed"
	}
}
