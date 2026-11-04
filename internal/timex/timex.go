// Package timex handles calendar dates, months and the clock.
//
// A date is a time.Time at midnight UTC: it names a calendar day, not an instant, so
// two dates compare with Equal and format with time.DateOnly regardless of the zone
// they came from.
package timex

import (
	"fmt"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/errors"
)

// Errors are of kind errors.ErrInvalid: they describe bad input.
var (
	ErrDate  = fmt.Errorf("must be YYYY-MM-DD: %w", errors.ErrInvalid)
	ErrMonth = fmt.Errorf("must be YYYY-MM: %w", errors.ErrInvalid)
	ErrClock = fmt.Errorf("must be HH:MM: %w", errors.ErrInvalid)
)

const (
	_monthLayout = "2006-01"
	_clockLayout = "15:04"
)

// Clock tells the current time. Services take a Clock so tests can fix it.
type Clock interface {
	Now() time.Time
}

// System is the real clock.
type System struct{}

// Now returns time.Now.
func (System) Now() time.Time { return time.Now() }

// Fixed is a clock stopped at a time, for tests.
type Fixed time.Time

// Now returns the fixed time.
func (f Fixed) Now() time.Time { return time.Time(f) }

// Date returns the calendar day of t in its own location.
func Date(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Today returns the calendar day of now in loc.
func Today(c Clock, loc *time.Location) time.Time {
	return Date(c.Now().In(loc))
}

// ParseDate parses "YYYY-MM-DD".
func ParseDate(s string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, ErrDate
	}
	return t, nil
}

// FormatDate formats a date as "YYYY-MM-DD", or "" for the zero time.
func FormatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}

// ParseClock parses a wall clock time "HH:MM" or "HH:MM:SS" and returns it as "HH:MM".
func ParseClock(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{_clockLayout, time.TimeOnly} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(_clockLayout), nil
		}
	}
	return "", ErrClock
}

// FormatClock formats the wall clock time of t as "HH:MM".
func FormatClock(t time.Time) string {
	return t.Format(_clockLayout)
}

// Month is a calendar month. The zero Month is invalid.
type Month struct {
	first time.Time
}

// ParseMonth parses "YYYY-MM".
func ParseMonth(s string) (Month, error) {
	t, err := time.Parse(_monthLayout, strings.TrimSpace(s))
	if err != nil {
		return Month{}, ErrMonth
	}
	return Month{first: t}, nil
}

// MonthOf returns the month of a date.
func MonthOf(t time.Time) Month {
	return Month{first: time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)}
}

// String returns "YYYY-MM".
func (m Month) String() string {
	return m.first.Format(_monthLayout)
}

// IsZero reports whether m is the zero Month.
func (m Month) IsZero() bool {
	return m.first.IsZero()
}

// Prev returns the month before m.
func (m Month) Prev() Month {
	return Month{first: m.first.AddDate(0, -1, 0)}
}

// Next returns the month after m.
func (m Month) Next() Month {
	return Month{first: m.first.AddDate(0, 1, 0)}
}

// First returns the first date of the month.
func (m Month) First() time.Time {
	return m.first
}

// Last returns the last date of the month.
func (m Month) Last() time.Time {
	return m.first.AddDate(0, 1, -1)
}
