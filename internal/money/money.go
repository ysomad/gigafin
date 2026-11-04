// Package money holds amounts of money and converts them to and from the integer
// minor units stored in the database.
package money

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/ysomad/gigafin/internal/errors"
)

// _exponents holds ISO 4217 minor unit digits for supported currencies.
var _exponents = map[string]int32{
	"AED": 2, "AMD": 2, "AUD": 2, "AZN": 2, "BHD": 3, "BYN": 2, "CAD": 2, "CHF": 2,
	"CNY": 2, "CZK": 2, "DKK": 2, "EUR": 2, "GBP": 2, "GEL": 2, "HKD": 2, "HUF": 2,
	"IDR": 2, "ILS": 2, "INR": 2, "IQD": 3, "JOD": 3, "JPY": 0, "KGS": 2, "KRW": 0,
	"KWD": 3, "KZT": 2, "MDL": 2, "MYR": 2, "NOK": 2, "NZD": 2, "OMR": 3, "PLN": 2,
	"RSD": 2, "RUB": 2, "SEK": 2, "SGD": 2, "THB": 2, "TJS": 2, "TND": 3, "TRY": 2,
	"UAH": 2, "USD": 2, "UZS": 2, "VND": 0,
}

// _cleaner drops digit group separators and normalizes the decimal point and minus.
var _cleaner = strings.NewReplacer(" ", "", "\u00a0", "", "\u202f", "", "\u2009", "", ",", ".", "\u2212", "-")

// Currencies returns supported currency codes, sorted.
func Currencies() []string {
	return slices.Sorted(maps.Keys(_exponents))
}

// Exponent returns the number of minor unit digits of the currency.
func Exponent(currency string) (int32, error) {
	exp, ok := _exponents[currency]
	if !ok {
		return 0, fmt.Errorf("unknown currency %q: %w", currency, errors.ErrInvalid)
	}
	return exp, nil
}

// Valid reports whether currency is supported.
func Valid(currency string) bool {
	_, ok := _exponents[currency]
	return ok
}

// Amount is an amount of money in whole currency units. The zero value is zero.
// The currency is kept by the caller.
type Amount struct {
	d decimal.Decimal
}

// NullAmount is an Amount that may be unset.
type NullAmount struct {
	Amount Amount
	Valid  bool
}

// FromInt returns v whole units.
func FromInt(v int64) Amount {
	return Amount{d: decimal.NewFromInt(v)}
}

// Parse parses user or bank input such as "1 040,50" or "-12.3". A comma is the decimal
// point, not a thousand separator. Errors are of kind errors.ErrInvalid.
func Parse(s string) (Amount, error) {
	clean := _cleaner.Replace(strings.TrimSpace(s))
	if clean == "" {
		return Amount{}, fmt.Errorf("required: %w", errors.ErrInvalid)
	}
	// decimal accepts exponents; 1e2000000000 would make ToMinor allocate gigabytes
	if strings.ContainsAny(clean, "eE") {
		return Amount{}, fmt.Errorf("%q is not a number: %w", s, errors.ErrInvalid)
	}
	d, err := decimal.NewFromString(clean)
	if err != nil {
		return Amount{}, fmt.Errorf("%q is not a number: %w", s, errors.ErrInvalid)
	}
	return Amount{d: d}, nil
}

// MustParse is Parse that panics on error, for tests and constants.
func MustParse(s string) Amount {
	a, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return a
}

// FromMinor converts minor units of currency to an amount. Unknown currencies are
// treated as having two decimal places.
func FromMinor(v int64, currency string) Amount {
	exp, err := Exponent(currency)
	if err != nil {
		exp = 2
	}
	return Amount{d: decimal.New(v, -exp)}
}

// ToMinor converts a to minor units of currency. It fails with errors.ErrInvalid if
// the currency is unknown or a has more decimal places than it allows.
func (a Amount) ToMinor(currency string) (int64, error) {
	exp, err := Exponent(currency)
	if err != nil {
		return 0, err
	}
	shifted := a.d.Shift(exp)
	if !shifted.Equal(shifted.Truncate(0)) {
		return 0, fmt.Errorf("%s: too many decimal places for %s: %w",
			a.d, currency, errors.ErrInvalid)
	}
	if !shifted.BigInt().IsInt64() {
		return 0, fmt.Errorf("%s: out of range: %w", a.d, errors.ErrInvalid)
	}
	return shifted.IntPart(), nil
}

// CeilMinor returns a in minor units of currency rounded up. Unknown currencies are
// treated as having two decimal places.
func (a Amount) CeilMinor(currency string) int64 {
	exp, err := Exponent(currency)
	if err != nil {
		exp = 2
	}
	return a.d.Shift(exp).Ceil().IntPart()
}

// FloorMinor returns a in minor units of currency rounded down. Unknown currencies are
// treated as having two decimal places.
func (a Amount) FloorMinor(currency string) int64 {
	exp, err := Exponent(currency)
	if err != nil {
		exp = 2
	}
	return a.d.Shift(exp).Floor().IntPart()
}

// Add returns a + b.
func (a Amount) Add(b Amount) Amount { return Amount{d: a.d.Add(b.d)} }

// Sub returns a - b.
func (a Amount) Sub(b Amount) Amount { return Amount{d: a.d.Sub(b.d)} }

// Neg returns -a.
func (a Amount) Neg() Amount { return Amount{d: a.d.Neg()} }

// Abs returns |a|.
func (a Amount) Abs() Amount { return Amount{d: a.d.Abs()} }

// Div returns a divided by n.
func (a Amount) Div(n int64) Amount { return Amount{d: a.d.Div(decimal.NewFromInt(n))} }

// PercentOf returns a as a share of total in percent, truncated toward zero. total
// must not be zero.
func (a Amount) PercentOf(total Amount) int64 {
	return a.d.Mul(decimal.NewFromInt(100)).Div(total.d).IntPart()
}

// Cmp returns -1, 0 or +1 as a is less than, equal to or greater than b.
func (a Amount) Cmp(b Amount) int { return a.d.Cmp(b.d) }

// Equal reports whether a equals b regardless of decimal places.
func (a Amount) Equal(b Amount) bool { return a.d.Equal(b.d) }

// IsZero reports whether a is zero.
func (a Amount) IsZero() bool { return a.d.IsZero() }

// IsWhole reports whether a has no fractional part.
func (a Amount) IsWhole() bool { return a.d.Equal(a.d.Truncate(0)) }

// IsNegative reports whether a is below zero.
func (a Amount) IsNegative() bool { return a.d.IsNegative() }

// IsPositive reports whether a is above zero.
func (a Amount) IsPositive() bool { return a.d.IsPositive() }

// Float64 returns a as a float for charts; it may lose precision.
func (a Amount) Float64() float64 { return a.d.InexactFloat64() }

// String returns a as a plain decimal, e.g. "-1040.5".
func (a Amount) String() string { return a.d.String() }

// Fixed formats a with the currency's decimal places, e.g. "1040.50".
func (a Amount) Fixed(currency string) string {
	exp, err := Exponent(currency)
	if err != nil {
		exp = 2
	}
	return a.d.StringFixed(exp)
}

// Format formats a for display with thin-space thousand groups, e.g. "-1 040.50".
func (a Amount) Format(currency string) string {
	return group(a.Fixed(currency))
}

// FormatWhole formats a rounded to whole units, half away from zero, for display with
// thin-space thousand groups, e.g. "-1 041".
func (a Amount) FormatWhole() string {
	return group(a.d.Round(0).StringFixed(0))
}

// MarshalJSON encodes a as a quoted decimal string.
func (a Amount) MarshalJSON() ([]byte, error) { return a.d.MarshalJSON() }

// UnmarshalJSON decodes a quoted or bare decimal.
func (a *Amount) UnmarshalJSON(b []byte) error { return a.d.UnmarshalJSON(b) }

// group inserts thin-space thousand groups into the integer part of the decimal s.
func group(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, hasFrac := strings.Cut(s, ".")

	var b strings.Builder
	if neg {
		b.WriteString("-")
	}
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString("\u202f")
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteString(".")
		b.WriteString(frac)
	}
	return b.String()
}
