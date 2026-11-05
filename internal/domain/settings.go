package domain

import (
	"slices"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/money"
)

// Settings are a user's preferences; the zero value is the default.
type Settings struct {
	// ShowMinorUnits shows amounts with minor units, such as kopecks, instead of
	// rounding them to whole units.
	ShowMinorUnits bool
	// RateSource is where exchange rates come from; rates are off by default.
	RateSource RateSource
	// PrimaryCurrency is the currency totals are converted to when rates are on.
	PrimaryCurrency string
	// Language is the UI language code, empty to follow the browser.
	Language string
}

// Validate checks that s names a known language and rate source and, when rates are
// on, a supported primary currency. Errors are of kind errors.ErrInvalid.
func (s Settings) Validate() error {
	if s.Language != "" &&
		!slices.ContainsFunc(i18n.Languages(), func(l i18n.Language) bool { return l.Code == s.Language }) {
		return NewError(errors.ErrInvalid, "language: unknown %q", s.Language)
	}
	if _, err := ParseRateSource(string(s.RateSource)); err != nil {
		return err
	}
	if s.RateSource == RateSourceNone {
		return nil
	}
	if s.PrimaryCurrency == "" {
		return NewError(errors.ErrInvalid, "primary currency: required with exchange rates on")
	}
	if !money.ValidCurrency(s.PrimaryCurrency) {
		return NewError(errors.ErrInvalid, "primary currency: unknown %q", s.PrimaryCurrency)
	}
	return nil
}
