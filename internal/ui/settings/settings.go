package settings

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the settings page and the actions of its cards.
type Handler struct {
	app *kit.App
}

// New returns the handler of settings.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of settings.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /settings":                       h.settings,
		"POST /settings":                      h.saveSettings,
		"POST /settings/rates/sync":           h.syncRates,
		"POST /settings/rates/set":            h.setRate,
		"POST /settings/rates/reset":          h.resetRate,
		"POST /settings/password":             h.changePassword,
		"POST /settings/api-keys":             h.createAPIKey,
		"POST /settings/delete-data":          h.deleteAllData,
		"GET /settings/export":                h.exportData,
		"POST /settings/backup":               h.backup,
		"POST /settings/api-keys/{id}/revoke": h.revokeAPIKey,
		"POST /settings/sessions/{id}/revoke": h.revokeSession,
	}
}

// settingsCard is a set of cards of the settings page an htmx post swaps in.
type settingsCard int

const (
	_cardGeneral settingsCard = 1 << iota
	_cardPassword
	_cardRates
	_cardAccess
	_cardDelete
	_cardAll = _cardGeneral | _cardPassword | _cardRates | _cardAccess | _cardDelete
)

// loadSettings sets the data of cards in d. The settings are read here, not taken from
// the request prefs, since a post may have just changed them.
func (h *Handler) loadSettings(r *http.Request, user domain.User, d *pageData, cards settingsCard) error {
	ctx := r.Context()
	var err error
	if cards&_cardAccess != 0 {
		if d.Sessions, err = h.app.Svc.Auth.ListSessions(ctx, user.ID, h.app.SessionToken(r)); err != nil {
			return err
		}
		if d.Keys, err = h.app.Svc.Auth.ListAPIKeys(ctx, user.ID); err != nil {
			return err
		}
	}
	if cards&(_cardGeneral|_cardRates) != 0 {
		if d.Settings, err = h.app.Svc.Settings.Get(ctx, user.ID); err != nil {
			return err
		}
		d.Currencies = money.Currencies()
	}
	if cards&_cardGeneral != 0 {
		d.Languages, d.RateSources = i18n.Languages(), domain.RateSources()
		if d.General == nil {
			s := d.Settings
			d.General = &generalForm{
				Timezone: user.Timezone, Language: s.Language, RateSource: string(s.RateSource),
				PrimaryCurrency: s.PrimaryCurrency, ShowMinorUnits: s.ShowMinorUnits, AutoCategoryRules: s.AutoCategoryRules,
			}
		}
	}
	if cards&_cardRates != 0 {
		if d.Rates, err = h.app.Svc.Rates.Latest(ctx, user.ID); err != nil {
			return err
		}
		if d.RateCurrencies, err = h.app.Svc.Rates.Currencies(ctx, user.ID); err != nil {
			return err
		}
	}
	return nil
}

// renderSettings renders the settings page with d, of which it sets the data of every
// card. The page has several forms with the same input names, so kit.FieldsError of
// the form named fail.Form are keyed form.input.
func (h *Handler) renderSettings(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, d pageData, fail kit.Failure,
) error {
	if err := h.loadSettings(r, user, &d, _cardAll); err != nil {
		return err
	}
	d.CanBackup = user.Can(domain.PermBackup)
	return kit.Render(h.app, w, r, status, page, kit.Page{User: &user, Nav: "settings", Title: "Settings"}, d, fail)
}

// settingsReply answers a settings form post that failed with fail.Err, or succeeded
// when it is nil. htmx gets cards, swapped in out of band, and the toasts. A plain
// browser gets the page on failure or with a result shown once, a new API key token or
// a rate sync, else is sent back to it.
func (h *Handler) settingsReply(
	w http.ResponseWriter, r *http.Request, user domain.User, cards settingsCard, d pageData, fail kit.Failure,
) error {
	status := http.StatusOK
	if fail.Err != nil {
		status = http.StatusUnprocessableEntity
		if _, ok := errors.AsType[kit.FieldsError](fail.Err); !ok && !kit.IsUserError(fail.Err) {
			return fail.Err
		}
	}
	if !kit.IsHTMX(r) {
		// a token is never put in a URL, so it is rendered instead of redirecting
		if fail.Err != nil || d.NewToken != "" || d.RatesSync.Done {
			return h.renderSettings(w, r, user, status, d, fail)
		}
		kit.Redirect(w, r, "/settings")
		return nil
	}
	if err := h.loadSettings(r, user, &d, cards); err != nil {
		return err
	}
	v := kit.Page{User: &user, Errors: h.app.FormErrors(w, r, fail)}
	var cs []templ.Component
	if cards&_cardGeneral != 0 {
		cs = append(cs, generalCard(v, d, true))
	}
	if cards&_cardPassword != 0 {
		cs = append(cs, passwordCard(v, true))
	}
	if cards&_cardRates != 0 {
		cs = append(cs, ratesCard(v, d, true))
	}
	if cards&_cardAccess != 0 {
		cs = append(cs, accessCard(v, d, true))
	}
	if cards&_cardDelete != 0 {
		cs = append(cs, deleteCard(v, true))
	}
	cs = append(cs, kit.Toasts(v, true, toasts(d)))
	return h.app.Fragment(w, r, status, templ.Join(cs...))
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return h.renderSettings(w, r, user, http.StatusOK, pageData{}, kit.Failure{})
}

// saveSettings stores the general settings form as a whole: the time zone, kept with
// the user, and every domain.Settings field.
func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f generalForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	f.Timezone = strings.TrimSpace(f.Timezone)
	errs := kit.FieldsError{}
	if _, err := domain.ParseTimezone(f.Timezone); err != nil {
		errs["settings.timezone"] = domain.NewError(errors.ErrInvalid, "unknown time zone")
	}
	s := domain.Settings{
		ShowMinorUnits:    f.ShowMinorUnits,
		PrimaryCurrency:   f.PrimaryCurrency,
		Language:          f.Language,
		AutoCategoryRules: f.AutoCategoryRules,
	}
	known := slices.ContainsFunc(i18n.Languages(), func(l i18n.Language) bool { return l.Code == s.Language })
	if s.Language != "" && !known {
		errs["settings.language"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	src, err := domain.ParseRateSource(f.RateSource)
	if err != nil {
		errs["settings.rate_source"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	s.RateSource = src
	switch {
	case err != nil || src == domain.RateSourceNone:
	case s.PrimaryCurrency == "":
		errs["settings.primary_currency"] = domain.NewError(errors.ErrInvalid, "required with exchange rates on")
	case !money.ValidCurrency(s.PrimaryCurrency):
		errs["settings.primary_currency"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	posted := pageData{General: &f}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, _cardGeneral, posted, kit.Failure{Err: errs, Form: "settings"})
	}
	if err := h.app.Svc.Auth.SetTimezone(r.Context(), user.ID, f.Timezone); err != nil {
		return h.settingsReply(w, r, user, _cardGeneral, posted, kit.Failure{Err: err, Form: "settings"})
	}
	// user is read before the change, and the cards show dates in its time zone
	user.Timezone = f.Timezone
	if err := h.app.Svc.Settings.Set(r.Context(), user.ID, s); err != nil {
		return h.settingsReply(w, r, user, _cardGeneral, posted, kit.Failure{Err: err, Form: "settings"})
	}
	if s.Language != kit.PrefsFrom(r.Context()).Settings.Language {
		// the whole page changes language, so it is loaded again
		kit.Redirect(w, r, "/settings")
		return nil
	}
	return h.settingsReply(w, r, user, _cardGeneral|_cardRates|_cardAccess, pageData{}, kit.Failure{})
}

func (h *Handler) syncRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	n, err := h.app.Svc.Rates.Sync(r.Context())
	// a failing source is not a fault of this server, so it is shown, not a 500
	if err != nil {
		h.app.Log.WarnContext(r.Context(), "sync exchange rates", slog.Any("error", err))
	}
	d := pageData{RatesSync: ratesSync{Done: true, Updated: n, Err: err}}
	return h.settingsReply(w, r, user, _cardRates, d, kit.Failure{})
}

func (h *Handler) setRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f rateForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	errs := kit.FieldsError{}
	if !money.ValidCurrency(f.Currency) {
		errs["rate.currency"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if _, err := timex.ParseDate(f.Date); err != nil {
		errs["rate.date"] = domain.NewError(errors.ErrInvalid, "must be YYYY-MM-DD")
	}
	switch v, err := money.Parse(f.Rate); {
	case err != nil:
		errs["rate.rate"] = domain.NewError(errors.ErrInvalid, "enter a number")
	case !v.IsPositive():
		errs["rate.rate"] = domain.NewError(errors.ErrInvalid, "must be positive")
	}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, _cardRates, pageData{Rate: f}, kit.Failure{Err: errs, Form: "rate"})
	}
	if err := h.app.Svc.Rates.Set(r.Context(), user.ID, f.Currency, f.Date, f.Rate); err != nil {
		return h.settingsReply(w, r, user, _cardRates, pageData{Rate: f}, kit.Failure{Err: err, Form: "rate"})
	}
	return h.settingsReply(w, r, user, _cardRates, pageData{}, kit.Failure{})
}

func (h *Handler) resetRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := h.app.Svc.Rates.Reset(
		r.Context(),
		user.ID,
		r.PostFormValue("currency"),
		r.PostFormValue("date"),
	); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, _cardRates, pageData{}, kit.Failure{})
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	current, pass := r.PostFormValue("current"), r.PostFormValue("password")
	errs := kit.FieldsError{}
	if current == "" {
		errs["password.current"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if err := domain.CheckPasswordPolicy(pass); err != nil {
		errs["password.password"] = err
	} else if pass == current {
		errs["password.password"] = domain.NewError(errors.ErrInvalid, "must differ from the current password")
	}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, _cardPassword, pageData{}, kit.Failure{Err: errs, Form: "password"})
	}
	token, err := h.app.Svc.Auth.ChangePassword(r.Context(), user.ID, current, pass, r.UserAgent())
	if errors.Is(err, errors.ErrUnauthorized) {
		errs["password.current"] = domain.NewError(errors.ErrInvalid, "wrong password")
		return h.settingsReply(w, r, user, _cardPassword, pageData{}, kit.Failure{Err: errs, Form: "password"})
	}
	if err != nil {
		return h.settingsReply(w, r, user, _cardPassword, pageData{}, kit.Failure{Err: err, Form: "password"})
	}
	// all sessions end on a password change, this browser gets a new one
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, token, int(h.app.SessionTTL.Seconds())))
	p := kit.PrefsFrom(r.Context())
	notice := h.app.Dicts.T(p.Lang, "Password changed. Other sessions are signed out.")
	if !kit.IsHTMX(r) {
		h.app.SetNotice(w, notice)
	}
	// the request still carries the old session cookie, so the sessions are listed
	// without the current one marked
	return h.settingsReply(w, r, user, _cardPassword|_cardAccess, pageData{Notice: notice}, kit.Failure{})
}

func (h *Handler) createAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f apiKeyForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	errs := kit.FieldsError{}
	switch name := strings.TrimSpace(f.Name); {
	case name == "":
		errs["apikey.name"] = domain.NewError(errors.ErrInvalid, "required")
	case utf8.RuneCountInString(name) > domain.MaxTextLen:
		errs["apikey.name"] = domain.NewError(errors.ErrInvalid, "too long")
	}
	if _, err := domain.ParseScope(f.Scope); err != nil {
		errs["apikey.scope"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if _, err := domain.ParseKeyTTL(f.Expires); err != nil {
		errs["apikey.expires"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if r.PostFormValue("password") == "" {
		errs["apikey.password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, _cardAccess, pageData{APIKey: f}, kit.Failure{Err: errs, Form: "apikey"})
	}
	token, _, err := h.app.Svc.Auth.CreateAPIKey(r.Context(), user.ID, r.PostFormValue("password"),
		f.Name, domain.Scope(f.Scope), f.Expires)
	if err != nil {
		return h.settingsReply(w, r, user, _cardAccess, pageData{APIKey: f}, kit.Failure{Err: err, Form: "apikey"})
	}
	return h.settingsReply(w, r, user, _cardAccess, pageData{NewToken: token}, kit.Failure{})
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Auth.RevokeAPIKey(r.Context(), user.ID, id); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, _cardAccess, pageData{}, kit.Failure{})
}

func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Auth.RevokeSession(r.Context(), user.ID, id); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, _cardAccess, pageData{}, kit.Failure{})
}

func (h *Handler) exportData(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	if f := q.Get("format"); f != "csv" {
		return domain.NewError(errors.ErrInvalid, "format: unsupported %q", f)
	}
	f, err := h.app.FilterFromQuery(q)
	if err != nil {
		return err
	}
	// buffered so a failure still renders as an error page
	var buf bytes.Buffer
	if err := h.app.Svc.Data.Export(r.Context(), user.ID, f, &buf); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="gigafin-`+timex.FormatDate(h.app.Svc.Today(user))+`.csv"`)
	// the response has started, so a failed write, mostly a gone client, is only logged
	if _, err := buf.WriteTo(w); err != nil {
		h.app.Log.DebugContext(r.Context(), "write export", slog.Any("error", err))
	}
	return nil
}

func (h *Handler) backup(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	pass, passphrase := r.PostFormValue("password"), r.PostFormValue("passphrase")
	// the form posts without htmx, for its file, so an error page is a full page load
	errs := kit.FieldsError{}
	if pass == "" {
		errs["backup.password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if passphrase == "" {
		errs["backup.passphrase"] = domain.NewError(errors.ErrInvalid, "required")
	} else if err := domain.CheckPasswordPolicy(passphrase); err != nil {
		errs["backup.passphrase"] = err
	}
	if len(errs) > 0 {
		return h.renderSettings(w, r, user, http.StatusUnprocessableEntity, pageData{},
			kit.Failure{Err: errs, Form: "backup"})
	}
	ctx := r.Context()
	dir, err := os.MkdirTemp("", "gigafin-backup-*")
	if err != nil {
		return fmt.Errorf("backup dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			h.app.Log.ErrorContext(ctx, "remove backup dir", slog.String("dir", dir), slog.Any("error", err))
		}
	}()
	f, err := h.app.Svc.Data.Backup(ctx, user, pass, passphrase, dir)
	if errors.Is(err, errors.ErrUnauthorized) {
		errs["backup.password"] = domain.NewError(errors.ErrInvalid, "wrong password")
		return h.renderSettings(w, r, user, http.StatusUnprocessableEntity, pageData{},
			kit.Failure{Err: errs, Form: "backup"})
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
		`attachment; filename="gigafin-`+timex.FormatDate(h.app.Svc.Today(user))+`.db.age"`)
	http.ServeContent(w, r, "", fi.ModTime(), f)
	return nil
}

func (h *Handler) deleteAllData(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	// finish even if the browser gives up waiting, so the result does not depend on it
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	if r.PostFormValue("password") == "" {
		errs := kit.FieldsError{"delete.password": domain.NewError(errors.ErrInvalid, "required")}
		return h.settingsReply(w, r, user, _cardDelete, pageData{}, kit.Failure{Err: errs, Form: "delete"})
	}
	if err := h.app.Svc.Data.DeleteAll(ctx, user.ID, r.PostFormValue("password")); err != nil {
		return h.settingsReply(w, r, user, _cardDelete, pageData{}, kit.Failure{Err: err, Form: "delete"})
	}
	kit.Redirect(w, r, "/accounts")
	return nil
}
