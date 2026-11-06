package ui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

type settingsData struct {
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
	RatesSync   ratesSync
	// RateCurrencies are the currencies the user needs rates of
	RateCurrencies []string
}

// ratesSync is the result of a rate sync, shown on the settings page as toasts.
type ratesSync struct {
	Done    bool
	Updated int
	// Err is a failure of some sources; the rest are synced
	Err error
}

// renderSettings renders the settings page with d, of which it sets all but NewToken,
// RatesSync and Posted. The page has several forms with the same input names, so
// fieldsError of the form named form are keyed form.input.
func (u *UI) renderSettings(
	w http.ResponseWriter,
	r *http.Request,
	user domain.User,
	status int,
	d settingsData,
	form string,
	viewErr error,
) error {
	var session string
	if c, err := r.Cookie(u.cookie(_sessionCookie, "", 0).Name); err == nil {
		session = c.Value
	}
	sessions, err := u.svc.Auth.ListSessions(r.Context(), user.ID, session)
	if err != nil {
		return err
	}
	keys, err := u.svc.Auth.ListAPIKeys(r.Context(), user.ID)
	if err != nil {
		return err
	}
	settings, err := u.svc.Settings.Get(r.Context(), user.ID)
	if err != nil {
		return err
	}
	rates, err := u.svc.Rates.Latest(r.Context(), user.ID)
	if err != nil {
		return err
	}
	rateCurrencies, err := u.svc.Rates.Currencies(r.Context(), user.ID)
	if err != nil {
		return err
	}
	d.Sessions, d.Keys, d.CanBackup = sessions, keys, user.Can(domain.PermBackup)
	d.Settings, d.Rates, d.RateCurrencies = settings, rates, rateCurrencies
	d.Languages, d.RateSources, d.Currencies = i18n.Languages(), domain.RateSources(), money.Currencies()
	return u.render(w, r, status, "settings", view{
		User: &user, Nav: "settings", Title: "Settings", Error: viewErr, Form: form, Data: d,
	})
}

func (u *UI) settings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderSettings(w, r, user, http.StatusOK, settingsData{}, "", nil)
}

// settingsDone ends a settings form post: the page with the form's errors and values for
// a user error. On success htmx gets the page, of which the form swaps in just its own
// card, and a plain browser is sent back to the page.
func (u *UI) settingsDone(w http.ResponseWriter, r *http.Request, user domain.User, form string, err error) error {
	if isUserError(err) {
		return u.renderSettings(w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, form, err)
	}
	if err != nil {
		return err
	}
	if r.Header.Get("HX-Request") == "true" {
		return u.renderSettings(w, r, user, http.StatusOK, settingsData{}, "", nil)
	}
	redirect(w, r, "/settings")
	return nil
}

// saveSettings stores the general settings form as a whole: the time zone, kept with
// the user, and every domain.Settings field.
func (u *UI) saveSettings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	tz := strings.TrimSpace(r.PostFormValue("timezone"))
	if _, err := domain.ParseTimezone(tz); err != nil {
		errs["settings.timezone"] = u.dicts.T(p.lang, "unknown time zone")
	}
	s := domain.Settings{
		ShowMinorUnits:    r.PostFormValue("show_minor_units") == "on",
		PrimaryCurrency:   r.PostFormValue("primary_currency"),
		Language:          r.PostFormValue("language"),
		AutoCategoryRules: r.PostFormValue("auto_category_rules") == "on",
	}
	known := slices.ContainsFunc(i18n.Languages(), func(l i18n.Language) bool { return l.Code == s.Language })
	if s.Language != "" && !known {
		errs["settings.language"] = u.dicts.T(p.lang, "choose from the list")
	}
	src, err := domain.ParseRateSource(r.PostFormValue("rate_source"))
	if err != nil {
		errs["settings.rate_source"] = u.dicts.T(p.lang, "choose from the list")
	}
	s.RateSource = src
	switch {
	case err != nil || src == domain.RateSourceNone:
	case s.PrimaryCurrency == "":
		errs["settings.primary_currency"] = u.dicts.T(p.lang, "required with exchange rates on")
	case !money.ValidCurrency(s.PrimaryCurrency):
		errs["settings.primary_currency"] = u.dicts.T(p.lang, "choose from the list")
	}
	if len(errs) > 0 {
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "settings", errs,
		)
	}
	if err := u.svc.Auth.SetTimezone(r.Context(), user.ID, tz); err != nil {
		return u.settingsDone(w, r, user, "settings", err)
	}
	// user is read before the change, and the page shows dates in its time zone
	user.Timezone = tz
	if err := u.svc.Settings.Set(r.Context(), user.ID, s); err != nil {
		return u.settingsDone(w, r, user, "settings", err)
	}
	if s.Language != p.settings.Language {
		// the whole page changes language, so it is loaded again
		redirect(w, r, "/settings")
		return nil
	}
	return u.settingsDone(w, r, user, "settings", nil)
}

func (u *UI) syncRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	n, err := u.svc.Rates.Sync(r.Context())
	// a failing source is not a fault of this server, so it is shown, not a 500
	if err != nil {
		u.log.WarnContext(r.Context(), "sync exchange rates", slog.Any("error", err))
	}
	d := settingsData{RatesSync: ratesSync{Done: true, Updated: n, Err: err}}
	return u.renderSettings(w, r, user, http.StatusOK, d, "", nil)
}

func (u *UI) setRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	if !money.ValidCurrency(r.PostFormValue("currency")) {
		errs["rate.currency"] = u.dicts.T(p.lang, "choose from the list")
	}
	if _, err := timex.ParseDate(r.PostFormValue("date")); err != nil {
		errs["rate.date"] = u.dicts.T(p.lang, "must be YYYY-MM-DD")
	}
	switch v, err := money.Parse(r.PostFormValue("rate")); {
	case err != nil:
		errs["rate.rate"] = u.dicts.T(p.lang, "enter a number")
	case !v.IsPositive():
		errs["rate.rate"] = u.dicts.T(p.lang, "must be positive")
	}
	if len(errs) > 0 {
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "rate", errs,
		)
	}
	err := u.svc.Rates.Set(r.Context(), user.ID, r.PostFormValue("currency"), r.PostFormValue("date"),
		r.PostFormValue("rate"))
	return u.settingsDone(w, r, user, "rate", err)
}

func (u *UI) resetRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Rates.Reset(
		r.Context(),
		user.ID,
		r.PostFormValue("currency"),
		r.PostFormValue("date"),
	); err != nil {
		return err
	}
	return u.settingsDone(w, r, user, "", nil)
}

func (u *UI) changePassword(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	current, pass := r.PostFormValue("current"), r.PostFormValue("password")
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	if current == "" {
		errs["password.current"] = u.dicts.T(p.lang, "required")
	}
	if err := domain.CheckPasswordPolicy(pass); err != nil {
		errs["password.password"] = errText(u.dicts, p.lang, err)
	} else if pass == current {
		errs["password.password"] = u.dicts.T(p.lang, "must differ from the current password")
	}
	if len(errs) > 0 {
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "password", errs,
		)
	}
	token, err := u.svc.Auth.ChangePassword(r.Context(), user.ID, current, pass, r.UserAgent())
	if errors.Is(err, errors.ErrUnauthorized) {
		errs["password.current"] = u.dicts.T(p.lang, "wrong password")
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "password", errs,
		)
	}
	if err != nil {
		return u.settingsDone(w, r, user, "password", err)
	}
	// all sessions end on a password change, this browser gets a new one
	http.SetCookie(w, u.cookie(_sessionCookie, token, int(u.sessionTTL.Seconds())))
	notice := u.dicts.T(p.lang, "Password changed. Other sessions are signed out.")
	if r.Header.Get("HX-Request") == "true" {
		return u.renderSettings(w, r, user, http.StatusOK, settingsData{Notice: notice}, "", nil)
	}
	http.SetCookie(w, u.cookie(_noticeCookie, url.QueryEscape(notice), 60))
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) createAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	switch name := strings.TrimSpace(r.PostFormValue("name")); {
	case name == "":
		errs["apikey.name"] = u.dicts.T(p.lang, "required")
	case utf8.RuneCountInString(name) > domain.MaxTextLen:
		errs["apikey.name"] = u.dicts.T(p.lang, "too long")
	}
	if _, err := domain.ParseScope(r.PostFormValue("scope")); err != nil {
		errs["apikey.scope"] = u.dicts.T(p.lang, "choose from the list")
	}
	if _, err := domain.ParseKeyTTL(r.PostFormValue("expires")); err != nil {
		errs["apikey.expires"] = u.dicts.T(p.lang, "choose from the list")
	}
	if r.PostFormValue("password") == "" {
		errs["apikey.password"] = u.dicts.T(p.lang, "required")
	}
	if len(errs) > 0 {
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "apikey", errs,
		)
	}
	token, _, err := u.svc.Auth.CreateAPIKey(r.Context(), user.ID, r.PostFormValue("password"),
		r.PostFormValue("name"), domain.Scope(r.PostFormValue("scope")), r.PostFormValue("expires"))
	if err != nil {
		return u.settingsDone(w, r, user, "apikey", err)
	}
	// render instead of redirecting so the token is shown once and never put in a URL
	return u.renderSettings(w, r, user, http.StatusOK, settingsData{NewToken: token}, "", nil)
}

func (u *UI) revokeAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Auth.RevokeAPIKey(r.Context(), user.ID, id); err != nil {
		return err
	}
	return u.settingsDone(w, r, user, "", nil)
}

func (u *UI) revokeSession(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Auth.RevokeSession(r.Context(), user.ID, id); err != nil {
		return err
	}
	return u.settingsDone(w, r, user, "", nil)
}

func (u *UI) exportData(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	if f := q.Get("format"); f != "csv" {
		return domain.NewError(errors.ErrInvalid, "format: unsupported %q", f)
	}
	f, err := u.filterFromQuery(q)
	if err != nil {
		return err
	}
	// buffered so a failure still renders as an error page
	var buf bytes.Buffer
	if err := u.svc.Data.Export(r.Context(), user.ID, f, &buf); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="gigafin-`+timex.FormatDate(u.svc.Today(user))+`.csv"`)
	// the response has started, so a failed write, mostly a gone client, is only logged
	if _, err := buf.WriteTo(w); err != nil {
		u.log.DebugContext(r.Context(), "write export", slog.Any("error", err))
	}
	return nil
}

func (u *UI) backup(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	pass, passphrase := r.PostFormValue("password"), r.PostFormValue("passphrase")
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	// the form posts without htmx, for its file, so an error page is a full page load
	errs := fieldsError{}
	if pass == "" {
		errs["backup.password"] = u.dicts.T(p.lang, "required")
	}
	if passphrase == "" {
		errs["backup.passphrase"] = u.dicts.T(p.lang, "required")
	} else if err := domain.CheckPasswordPolicy(passphrase); err != nil {
		errs["backup.passphrase"] = errText(u.dicts, p.lang, err)
	}
	if len(errs) > 0 {
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "backup", errs,
		)
	}
	ctx := r.Context()
	dir, err := os.MkdirTemp("", "gigafin-backup-*")
	if err != nil {
		return fmt.Errorf("backup dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			u.log.ErrorContext(ctx, "remove backup dir", slog.String("dir", dir), slog.Any("error", err))
		}
	}()
	f, err := u.svc.Data.Backup(ctx, user, pass, passphrase, dir)
	if errors.Is(err, errors.ErrUnauthorized) {
		errs["backup.password"] = u.dicts.T(p.lang, "wrong password")
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "backup", errs,
		)
	}
	if err != nil {
		// forbidden goes back to the settings page as a toast, where the form is not shown
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat backup: %w", err)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		`attachment; filename="gigafin-`+timex.FormatDate(u.svc.Today(user))+`.db.age"`)
	http.ServeContent(w, r, "", fi.ModTime(), f)
	return nil
}

func (u *UI) deleteAllData(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	// finish even if the browser gives up waiting, so the result does not depend on it
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	if r.PostFormValue("password") == "" {
		p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
		errs := fieldsError{"delete.password": u.dicts.T(p.lang, "required")}
		return u.renderSettings(
			w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, "delete", errs,
		)
	}
	if err := u.svc.Data.DeleteAll(ctx, user.ID, r.PostFormValue("password")); err != nil {
		return u.settingsDone(w, r, user, "delete", err)
	}
	redirect(w, r, "/accounts")
	return nil
}
