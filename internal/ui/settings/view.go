package settings

import (
	"fmt"
	"strings"

	"rsc.io/qr"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/totp"
)

// pageData is the data of the settings page, or of the cards of it an htmx post
// swaps in; a card's data is set only when it is rendered.
type pageData struct {
	Sessions  []domain.Session
	Keys      []domain.APIKey
	NewToken  string
	CanBackup bool
	// TwoFactor is the state of two-factor authentication. TOTPSetup is the secret of it
	// being set up, RecoveryCodes are new codes, shown once.
	TwoFactor     domain.TwoFactor
	TOTPSetup     *totpSetup
	RecoveryCodes []string
	// Notice is a success message of an htmx post, shown as a toast.
	Notice string
	// General, Rate and APIKey are the values of a failed post of their form, to fill
	// its inputs again; General is set to the stored settings when not posted.
	General *generalForm
	Rate    rateForm
	APIKey  apiKeyForm

	Settings    domain.Settings
	Languages   []i18n.Language
	RateSources []domain.RateSource
	Themes      []domain.Theme
	Currencies  []string
	Rates       []domain.Rate
	RatesSync   ratesSync
	// RateCurrencies are the currencies the user needs rates of
	RateCurrencies []string

	// Banks is the state of the banks card.
	Banks banksData
}

// banksData is the state of the banks card: a section per enabled bank and the
// accounts at each, with the accounts they sync with.
type banksData struct {
	Banks []bankData
	// Posted is the bank and phone of a sign-in form posted with an error, to fill it
	// again.
	Posted   bankData
	Accounts []domain.BankAccount
	// Linked are the accounts by id.
	Linked map[int64]domain.Account
	// Choices are the accounts no bank account syncs with, by the ids of the bank
	// accounts of their currency not synced, to sync one with; Suggested is the one
	// picked first, 0 for a new account.
	Choices   map[int64][]domain.Account
	Suggested map[int64]int64
	// SyncFailed marks a failed sync of an htmx post, shown as a toast; the error is
	// logged, not shown, as it may carry what the bank answered.
	SyncFailed bool
}

// bankData is the state of the section of a bank in the banks card.
type bankData struct {
	Bank domain.Bank
	// Conn is the stored connection, of an empty Status when there is none.
	Conn domain.BankConnection
	// Step is the input a sign-in in progress waits for, empty when none.
	Step string
	// Phone fills the phone input again, the stored one when not posted.
	Phone string
}

// generalForm is decoded from the general settings form.
type generalForm struct {
	Timezone          string `form:"timezone"`
	Language          string `form:"language"`
	RateSource        string `form:"rate_source"`
	PrimaryCurrency   string `form:"primary_currency"`
	ShowMinorUnits    bool   `form:"show_minor_units"`
	AutoCategoryRules bool   `form:"auto_category_rules"`
	Theme             string `form:"theme"`
}

// rateForm is decoded from the form setting an exchange rate by hand, and from the one
// resetting such a rate, which posts no Rate.
type rateForm struct {
	Currency string `form:"currency"`
	Date     string `form:"date"`
	Rate     string `form:"rate"`
}

// apiKeyForm is decoded from the form creating an API key. Password is cleared before
// the form is filled again, never shown.
type apiKeyForm struct {
	Name     string `form:"name"`
	Scope    string `form:"scope"`
	Expires  string `form:"expires"`
	Password string `form:"password"`
}

// passwordForm is decoded from the form changing the password.
type passwordForm struct {
	Current       string `form:"current"`
	Password      string `form:"password"`
	RevokeAPIKeys bool   `form:"revoke_api_keys"`
}

// twoFactorForm is decoded from the forms turning two-factor authentication on and
// managing it, where Action is what to do.
type twoFactorForm struct {
	Code     string `form:"code"`
	Password string `form:"password"`
	Action   string `form:"action"`
}

// backupForm is decoded from the form downloading a backup.
type backupForm struct {
	Password   string `form:"password"`
	Passphrase string `form:"passphrase"`
}

// deleteForm is decoded from the form deleting all data.
type deleteForm struct {
	Password string `form:"password"`
}

// ratesSync is the result of a rate sync, shown on the settings page as toasts.
type ratesSync struct {
	Done    bool
	Updated int
	// Err is a failure of some sources; the rest are synced
	Err error
}

// totpSetup is a TOTP secret being set up, with the QR code of its otpauth URI drawn as
// an SVG path of unit squares on a QRSize grid.
type totpSetup struct {
	Secret string
	// Key is Secret in groups of 4 to type by hand; apps ignore the spaces.
	Key    string
	QRPath string
	QRSize int
}

// newTOTPSetup returns the setup of secret for the user of login.
func newTOTPSetup(login, secret string) (*totpSetup, error) {
	// quiet is the blank border scanners need around a QR code, in modules
	const quiet = 4
	code, err := qr.Encode(totp.URI("gigafin", login, secret), qr.M)
	if err != nil {
		return nil, fmt.Errorf("encode qr code: %w", err)
	}
	var path strings.Builder
	for y := range code.Size {
		for x := range code.Size {
			if code.Black(x, y) {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	var key strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			key.WriteByte(' ')
		}
		key.WriteRune(r)
	}
	return &totpSetup{Secret: secret, Key: key.String(), QRPath: path.String(), QRSize: code.Size + 2*quiet}, nil
}
