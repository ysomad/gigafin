package ui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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

// ratesSync is the result of a rate sync, shown on the settings page.
type ratesSync struct {
	Done    bool
	Updated int
	// Err is a failure of some sources; the rest are synced
	Err error
}

// renderSettings renders the settings page with d, of which it sets all but NewToken,
// RatesSync and Posted. The page has several forms with the same input names, so the
// field of viewErr, an error of the form named form, becomes form.field.
func (u *UI) renderSettings(
	w http.ResponseWriter,
	r *http.Request,
	user domain.User,
	status int,
	d settingsData,
	form string,
	viewErr error,
) error {
	if e, ok := errors.AsType[domain.Error](viewErr); ok && e.Field != "" {
		e.Field = form + "." + e.Field
		viewErr = e
	}
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
		User: &user, Nav: "settings", Title: "Settings", Error: viewErr, Data: d,
	})
}

func (u *UI) settings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderSettings(w, r, user, http.StatusOK, settingsData{}, "", nil)
}

// settingsDone ends a settings form post: back to the page on success, the page with
// the form's errors and values for a user error.
func (u *UI) settingsDone(w http.ResponseWriter, r *http.Request, user domain.User, form string, err error) error {
	if isUserError(err) {
		return u.renderSettings(w, r, user, http.StatusUnprocessableEntity, settingsData{Posted: r.PostForm}, form, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) setTimezone(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	err := u.svc.Auth.SetTimezone(r.Context(), user.ID, strings.TrimSpace(r.PostFormValue("timezone")))
	return u.settingsDone(w, r, user, "timezone", err)
}

func (u *UI) setDisplay(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Settings.SetDisplay(r.Context(), user.ID, r.PostFormValue("show_minor_units") == "on"); err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) setLanguage(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	err := u.svc.Settings.SetLanguage(r.Context(), user.ID, r.PostFormValue("language"))
	return u.settingsDone(w, r, user, "language", err)
}

func (u *UI) setRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	src, err := domain.ParseRateSource(r.PostFormValue("rate_source"))
	if err == nil {
		err = u.svc.Settings.SetRates(r.Context(), user.ID, src, r.PostFormValue("primary_currency"))
	}
	return u.settingsDone(w, r, user, "rates", err)
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
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) changePassword(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	token, err := u.svc.Auth.ChangePassword(r.Context(), user.ID, r.PostFormValue("current"),
		r.PostFormValue("password"), r.UserAgent())
	if err != nil {
		return u.settingsDone(w, r, user, "password", err)
	}
	// all sessions end on a password change, this browser gets a new one
	http.SetCookie(w, u.cookie(_sessionCookie, token, int(u.sessionTTL.Seconds())))
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) createAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
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
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) revokeSession(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Auth.RevokeSession(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) exportData(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	if f := q.Get("format"); f != "csv" {
		return domain.NewFieldError(errors.ErrInvalid, "format", "unsupported %q", f)
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
	f, err := u.svc.Data.Backup(ctx, user, r.PostFormValue("password"), r.PostFormValue("passphrase"), dir)
	if err != nil {
		return u.settingsDone(w, r, user, "backup", err)
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
	if err := u.svc.Data.DeleteAll(ctx, user.ID, r.PostFormValue("password")); err != nil {
		return u.settingsDone(w, r, user, "delete", err)
	}
	redirect(w, r, "/accounts")
	return nil
}
