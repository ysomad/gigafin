package settings

import (
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/i18n"
)

// pageData is the data of the settings page, or of the cards of it an htmx post
// swaps in; a card's data is set only when it is rendered.
type pageData struct {
	Sessions  []domain.Session
	Keys      []domain.APIKey
	NewToken  string
	CanBackup bool
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
	Currencies  []string
	Rates       []domain.Rate
	RatesSync   ratesSync
	// RateCurrencies are the currencies the user needs rates of
	RateCurrencies []string

	// Banks is the state of the banks card.
	Banks banksData
}

// banksData is the state of the banks card: a section per bank and the accounts at
// each, with the accounts they sync with.
type banksData struct {
	TBank    tbankData
	Accounts []domain.BankAccount
	// Linked are the accounts by id.
	Linked map[int64]domain.Account
	// Choices are the accounts no bank account syncs with, by the ids of the bank
	// accounts of their currency not synced, to sync one with; Suggested is the one
	// picked first, 0 for a new account.
	Choices   map[int64][]domain.Account
	Suggested map[int64]int64
	// SyncErr is a failed sync of an htmx post, shown as a toast.
	SyncErr string
}

// tbankData is the state of the T-Bank section of the banks card.
type tbankData struct {
	Enabled bool
	// Connected is set when Conn is stored.
	Connected bool
	Conn      domain.BankConnection
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
}

// rateForm is decoded from the form setting an exchange rate by hand.
type rateForm struct {
	Currency string `form:"currency"`
	Date     string `form:"date"`
	Rate     string `form:"rate"`
}

// apiKeyForm is decoded from the form creating an API key, but for the password,
// which is never shown again.
type apiKeyForm struct {
	Name    string `form:"name"`
	Scope   string `form:"scope"`
	Expires string `form:"expires"`
}

// ratesSync is the result of a rate sync, shown on the settings page as toasts.
type ratesSync struct {
	Done    bool
	Updated int
	// Err is a failure of some sources; the rest are synced
	Err error
}
