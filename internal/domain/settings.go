package domain

import (
	"golang.org/x/text/language"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// Settings are a user's preferences, DefaultSettings until the user changes them.
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
	// AutoCategoryRules makes LearnCategory create rules when the user sets the category
	// of an operation by hand.
	AutoCategoryRules bool
}

// DefaultSettings returns the settings of a user who never changed them: rates off and
// rules learned from the user's categories.
func DefaultSettings() Settings {
	return Settings{AutoCategoryRules: true}
}

// Validate checks that s names a well-formed language tag, a known rate source and, when rates are
// on, a supported primary currency. Errors are of kind errors.ErrInvalid.
func (s Settings) Validate() error {
	if _, err := language.Parse(s.Language); s.Language != "" && err != nil {
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
