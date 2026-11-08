package view

import (
	"net/url"

	"github.com/a-h/templ"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/i18n"
)

type Settings struct {
	Sessions  []domain.Session
	Keys      []domain.APIKey
	NewToken  string
	CanBackup bool
	// Notice is a success message of an htmx post, shown as a toast.
	Notice string
	// Posted are the values of a form post that failed, to fill its inputs again.
	Posted url.Values

	Settings    domain.Settings
	Languages   []i18n.Language
	RateSources []domain.RateSource
	Currencies  []string
	Rates       []domain.Rate
	RatesSync   RatesSync
	// RateCurrencies are the currencies the user needs rates of
	RateCurrencies []string
}

// RatesSync is the result of a rate sync, shown on the settings page as toasts.
type RatesSync struct {
	Done    bool
	Updated int
	// Err is a failure of some sources; the rest are synced
	Err error
}

// HXCard returns the attributes of a settings form posting to action by htmx, which
// swaps in the card of id of the page the server answers with, and the elements of oob
// out of band.
func HXCard(action, id, oob string) templ.Attributes {
	return templ.Attributes{
		"hx-post":       action,
		"hx-target":     "#" + id,
		"hx-select":     "#" + id,
		"hx-swap":       "outerHTML ignoreTitle:true show:none",
		"hx-select-oob": oob,
	}
}
