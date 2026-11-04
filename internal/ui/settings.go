package ui

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
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

func (u *UI) renderSettings(
	w http.ResponseWriter,
	r *http.Request,
	user domain.User,
	token string,
	sync ratesSync,
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
	return u.render(w, r, http.StatusOK, "settings", view{
		User: &user, Nav: "settings", Title: "Settings",
		Data: settingsData{
			Sessions: sessions, Keys: keys, NewToken: token, CanBackup: user.Can(domain.PermBackup),
			Settings: settings, Languages: i18n.Languages(), RateSources: domain.RateSources(), Currencies: money.Currencies(),
			Rates: rates, RatesSync: sync, RateCurrencies: rateCurrencies,
		},
	})
}

func (u *UI) settings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderSettings(w, r, user, "", ratesSync{})
}

func (u *UI) setTimezone(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Auth.SetTimezone(r.Context(), user.ID, strings.TrimSpace(r.PostFormValue("timezone"))); err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
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
	if err := u.svc.Settings.SetLanguage(r.Context(), user.ID, r.PostFormValue("language")); err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) setRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	src, err := domain.ParseRateSource(r.PostFormValue("rate_source"))
	if err != nil {
		return err
	}
	err = u.svc.Settings.SetRates(r.Context(), user.ID, src, r.PostFormValue("primary_currency"))
	if err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
}

func (u *UI) syncRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	n, err := u.svc.Rates.Sync(r.Context())
	// a failing source is not a fault of this server, so it is shown, not a 500
	if err != nil {
		u.log.WarnContext(r.Context(), "sync exchange rates", slog.Any("error", err))
	}
	return u.renderSettings(w, r, user, "", ratesSync{Done: true, Updated: n, Err: err})
}

func (u *UI) setRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	err := u.svc.Rates.Set(r.Context(), user.ID, r.PostFormValue("currency"), r.PostFormValue("date"),
		r.PostFormValue("rate"))
	if err != nil {
		return err
	}
	redirect(w, r, "/settings")
	return nil
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
		return err
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
		return err
	}
	// render instead of redirecting so the token is shown once and never put in a URL
	return u.renderSettings(w, r, user, token, ratesSync{})
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
	if err := u.svc.Data.DeleteAll(ctx, user.ID, r.PostFormValue("password")); err != nil {
		return err
	}
	redirect(w, r, "/accounts")
	return nil
}
