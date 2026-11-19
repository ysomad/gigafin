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
		"GET /settings":                          h.settings,
		"POST /settings":                         h.saveSettings,
		"POST /settings/rates/sync":              h.syncRates,
		"POST /settings/rates/set":               h.setRate,
		"POST /settings/rates/reset":             h.resetRate,
		"POST /settings/password":                h.changePassword,
		"POST /settings/2fa/setup":               h.setupTwoFactor,
		"POST /settings/2fa/cancel":              h.cancelTwoFactorSetup,
		"POST /settings/2fa/enable":              h.enableTwoFactor,
		"POST /settings/2fa/manage":              h.manageTwoFactor,
		"POST /settings/api-keys":                h.createAPIKey,
		"POST /settings/delete-data":             h.deleteAllData,
		"GET /settings/export":                   h.exportData,
		"POST /settings/backup":                  h.backup,
		"POST /settings/api-keys/{id}/revoke":    h.revokeAPIKey,
		"POST /settings/sessions/{id}/revoke":    h.revokeSession,
		"POST /settings/banks/{bank}/login":      h.bankLogin,
		"POST /settings/banks/{bank}/confirm":    h.bankConfirm,
		"POST /settings/banks/{bank}/cancel":     h.bankCancel,
		"POST /settings/banks/{bank}/link":       h.bankLink,
		"POST /settings/banks/{bank}/disconnect": h.bankDisconnect,
		"POST /settings/banks/autosync":          h.banksAutoSync,
	}
}

// card is a card of the settings page: load sets its data in d, nil for a card of
// none, and render draws it to be swapped in out of band. Its data is read here, not
// taken from the request prefs, since a post may have just changed them.
type card struct {
	load   func(h *Handler, r *http.Request, user domain.User, d *pageData) error
	render func(v kit.Page, d pageData) templ.Component
}

var (
	_generalCard = card{
		load:   (*Handler).loadGeneral,
		render: func(v kit.Page, d pageData) templ.Component { return generalCard(v, d, true) },
	}
	_passwordCard = card{
		render: func(v kit.Page, _ pageData) templ.Component { return passwordCard(v, true) },
	}
	_twoFactorCard = card{
		load:   (*Handler).loadTwoFactor,
		render: func(v kit.Page, d pageData) templ.Component { return twoFactorCard(v, d, true) },
	}
	_ratesCard = card{
		load:   (*Handler).loadRates,
		render: func(v kit.Page, d pageData) templ.Component { return ratesCard(v, d, true) },
	}
	_accessCard = card{
		load:   (*Handler).loadAccess,
		render: func(v kit.Page, d pageData) templ.Component { return accessCard(v, d, true) },
	}
	_banksCard = card{
		load: func(h *Handler, r *http.Request, user domain.User, d *pageData) error {
			return h.loadBanks(r, user, &d.Banks)
		},
		render: func(v kit.Page, d pageData) templ.Component { return banksCard(v, d.Banks, true) },
	}
	_deleteCard = card{
		render: func(v kit.Page, _ pageData) templ.Component { return deleteCard(v, true) },
	}
)

func (h *Handler) loadGeneral(r *http.Request, user domain.User, d *pageData) error {
	var err error
	if d.Settings, err = h.app.Svc.Settings.Get(r.Context(), user.ID); err != nil {
		return err
	}
	d.Currencies = money.Currencies()
	d.Languages, d.RateSources, d.Themes = i18n.Languages(), domain.RateSources(), domain.Themes()
	if d.General == nil {
		s := d.Settings
		d.General = &generalForm{
			Timezone: user.Timezone, Language: s.Language, RateSource: string(s.RateSource),
			PrimaryCurrency: s.PrimaryCurrency, ShowMinorUnits: s.ShowMinorUnits, AutoCategoryRules: s.AutoCategoryRules,
			Theme: string(s.Theme),
		}
	}
	return nil
}

func (h *Handler) loadTwoFactor(r *http.Request, user domain.User, d *pageData) error {
	var err error
	if d.TwoFactor, err = h.app.Svc.Auth.TwoFactor(r.Context(), user.ID); err != nil {
		return err
	}
	if d.TwoFactor.PendingSecret != "" {
		if d.TOTPSetup, err = newTOTPSetup(user.Login, d.TwoFactor.PendingSecret); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) loadRates(r *http.Request, user domain.User, d *pageData) error {
	ctx := r.Context()
	var err error
	if d.Settings, err = h.app.Svc.Settings.Get(ctx, user.ID); err != nil {
		return err
	}
	d.Currencies = money.Currencies()
	if d.Rates, err = h.app.Svc.Rates.Latest(ctx, user.ID); err != nil {
		return err
	}
	if d.RateCurrencies, err = h.app.Svc.Rates.Currencies(ctx, user.ID); err != nil {
		return err
	}
	return nil
}

func (h *Handler) loadAccess(r *http.Request, user domain.User, d *pageData) error {
	ctx := r.Context()
	var err error
	if d.Sessions, err = h.app.Svc.Auth.ListSessions(ctx, user.ID, h.app.SessionToken(r)); err != nil {
		return err
	}
	if d.Keys, err = h.app.Svc.Auth.ListAPIKeys(ctx, user.ID); err != nil {
		return err
	}
	return nil
}

// renderSettings renders the settings page with d, of which it sets the data of every
// card. The page has several forms with the same input names, so kit.FieldsError of
// the form named fail.Form are keyed form.input.
func (h *Handler) renderSettings(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, d pageData, fail kit.Failure,
) error {
	for _, c := range []card{_generalCard, _twoFactorCard, _ratesCard, _accessCard, _banksCard} {
		if err := c.load(h, r, user, &d); err != nil {
			return err
		}
	}
	d.CanBackup = user.Can(domain.PermBackup)
	return kit.Render(h.app, w, r, status, page, kit.Page{User: &user, Nav: "settings", Title: "Settings"}, d, fail)
}

// settingsReply answers a settings form post that failed with fail.Err, or succeeded
// when it is nil. htmx gets cards, swapped in out of band, and the toasts. A plain
// browser gets the page on failure or with a result shown once, a new API key token,
// recovery codes or a rate sync, else is sent back to it.
func (h *Handler) settingsReply(
	w http.ResponseWriter, r *http.Request, user domain.User, d pageData, fail kit.Failure, cards ...card,
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
		if fail.Err != nil || d.NewToken != "" || d.RatesSync.Done || len(d.RecoveryCodes) > 0 {
			return h.renderSettings(w, r, user, status, d, fail)
		}
		kit.Redirect(w, r, "/settings")
		return nil
	}
	for _, c := range cards {
		if c.load == nil {
			continue
		}
		if err := c.load(h, r, user, &d); err != nil {
			return err
		}
	}
	return kit.Render(h.app, w, r, status, func(v kit.Page, d pageData) templ.Component {
		cs := make([]templ.Component, 0, len(cards)+1)
		for _, c := range cards {
			cs = append(cs, c.render(v, d))
		}
		return templ.Join(append(cs, kit.Toasts(v, true, toasts(d)))...)
	}, kit.Page{User: &user}, d, fail)
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
	theme, err := domain.ParseTheme(f.Theme)
	if err != nil {
		errs["settings.theme"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	s.Theme = theme
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
		return h.settingsReply(w, r, user, posted, kit.Failure{Err: errs, Form: "settings"}, _generalCard)
	}
	if err := h.app.Svc.Auth.SetTimezone(r.Context(), user.ID, f.Timezone); err != nil {
		return h.settingsReply(w, r, user, posted, kit.Failure{Err: err, Form: "settings"}, _generalCard)
	}
	// user is read before the change, and the cards show dates in its time zone
	user.Timezone = f.Timezone
	if err := h.app.Svc.Settings.Set(r.Context(), user.ID, s); err != nil {
		return h.settingsReply(w, r, user, posted, kit.Failure{Err: err, Form: "settings"}, _generalCard)
	}
	if prev := kit.PrefsFrom(r.Context()).Settings; s.Language != prev.Language || s.Theme != prev.Theme {
		// the whole page changes, <html> included, which htmx does not swap, so the
		// browser loads it again
		if kit.IsHTMX(r) {
			w.Header().Set("HX-Redirect", "/settings")
			w.WriteHeader(http.StatusOK)
			return nil
		}
		kit.Redirect(w, r, "/settings")
		return nil
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _generalCard, _ratesCard, _accessCard)
}

func (h *Handler) syncRates(w http.ResponseWriter, r *http.Request, user domain.User) error {
	n, err := h.app.Svc.Rates.SyncUser(r.Context(), user.ID)
	// a failing source is not a fault of this server, so it is shown, not a 500
	if err != nil {
		h.app.Log.WarnContext(r.Context(), "sync exchange rates", slog.Any("error", err))
	}
	d := pageData{RatesSync: ratesSync{Done: true, Updated: n, Err: err}}
	return h.settingsReply(w, r, user, d, kit.Failure{}, _ratesCard)
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
		return h.settingsReply(w, r, user, pageData{Rate: f}, kit.Failure{Err: errs, Form: "rate"}, _ratesCard)
	}
	if err := h.app.Svc.Rates.Set(r.Context(), user.ID, f.Currency, f.Date, f.Rate); err != nil {
		return h.settingsReply(w, r, user, pageData{Rate: f}, kit.Failure{Err: err, Form: "rate"}, _ratesCard)
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _ratesCard)
}

func (h *Handler) resetRate(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f rateForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	if err := h.app.Svc.Rates.Reset(r.Context(), user.ID, f.Currency, f.Date); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _ratesCard)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f passwordForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	current, pass := f.Current, f.Password
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
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: errs, Form: "password"}, _passwordCard)
	}
	token, err := h.app.Svc.Auth.ChangePassword(r.Context(), user.ID, current, pass, r.UserAgent(), f.RevokeAPIKeys)
	if errors.Is(err, errors.ErrUnauthorized) {
		errs["password.current"] = domain.NewError(errors.ErrInvalid, "wrong password")
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: errs, Form: "password"}, _passwordCard)
	}
	if err != nil {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: err, Form: "password"}, _passwordCard)
	}
	// all sessions end on a password change, this browser gets a new one
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, token, int(h.app.SessionTTL.Seconds())))
	p := kit.PrefsFrom(r.Context())
	notice := h.app.Dicts.T(p.Lang, "Password changed. Other sessions are signed out.")
	if f.RevokeAPIKeys {
		notice = h.app.Dicts.T(p.Lang, "Password changed. Other sessions are signed out and API keys revoked.")
	}
	if !kit.IsHTMX(r) {
		h.app.SetNotice(w, notice)
	}
	// the request still carries the old session cookie, so the sessions are listed
	// without the current one marked
	return h.settingsReply(w, r, user, pageData{Notice: notice}, kit.Failure{}, _passwordCard, _accessCard)
}

func (h *Handler) setupTwoFactor(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if _, err := h.app.Svc.Auth.SetupTwoFactor(r.Context(), user.ID); err != nil {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: err, Form: "2fa"}, _twoFactorCard)
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _twoFactorCard)
}

func (h *Handler) cancelTwoFactorSetup(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := h.app.Svc.Auth.CancelTwoFactorSetup(r.Context(), user.ID); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _twoFactorCard)
}

func (h *Handler) enableTwoFactor(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f twoFactorForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	code, pass := f.Code, f.Password
	errs := kit.FieldsError{}
	if strings.TrimSpace(code) == "" {
		errs["2fa.code"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if pass == "" {
		errs["2fa.password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: errs, Form: "2fa"}, _twoFactorCard)
	}
	codes, token, err := h.app.Svc.Auth.EnableTwoFactor(r.Context(), user.ID, pass, code, r.UserAgent())
	if err != nil {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: err, Form: "2fa"}, _twoFactorCard)
	}
	// sessions started with the password alone end, this browser gets a new one
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, token, int(h.app.SessionTTL.Seconds())))
	p := kit.PrefsFrom(r.Context())
	notice := h.app.Dicts.T(p.Lang, "Two-factor authentication is on. Other sessions are signed out.")
	return h.settingsReply(w, r, user, pageData{RecoveryCodes: codes, Notice: notice},
		kit.Failure{}, _twoFactorCard, _accessCard)
}

// manageTwoFactor makes new recovery codes or, for action "disable", turns two-factor
// authentication off; both need a code and the password, so they share the form.
func (h *Handler) manageTwoFactor(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f twoFactorForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	errs := kit.FieldsError{}
	if strings.TrimSpace(f.Code) == "" {
		errs["2fa-manage.code"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if f.Password == "" {
		errs["2fa-manage.password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: errs, Form: "2fa-manage"}, _twoFactorCard)
	}
	var codes []string
	var err error
	switch f.Action {
	case "disable":
		err = h.app.Svc.Auth.DisableTwoFactor(r.Context(), user.ID, f.Password, f.Code)
	case "recovery-codes":
		codes, err = h.app.Svc.Auth.RegenerateRecoveryCodes(r.Context(), user.ID, f.Password, f.Code)
	default:
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err != nil {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: err, Form: "2fa-manage"}, _twoFactorCard)
	}
	return h.settingsReply(w, r, user, pageData{RecoveryCodes: codes}, kit.Failure{}, _twoFactorCard)
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
	if f.Password == "" {
		errs["apikey.password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	pass := f.Password
	// the form is filled again on failure, but for the password
	f.Password = ""
	if len(errs) > 0 {
		return h.settingsReply(w, r, user, pageData{APIKey: f}, kit.Failure{Err: errs, Form: "apikey"}, _accessCard)
	}
	token, _, err := h.app.Svc.Auth.CreateAPIKey(r.Context(), user.ID, pass, f.Name, domain.Scope(f.Scope), f.Expires)
	if err != nil {
		return h.settingsReply(w, r, user, pageData{APIKey: f}, kit.Failure{Err: err, Form: "apikey"}, _accessCard)
	}
	return h.settingsReply(w, r, user, pageData{NewToken: token}, kit.Failure{}, _accessCard)
}

func (h *Handler) revokeAPIKey(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Auth.RevokeAPIKey(r.Context(), user.ID, id); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _accessCard)
}

func (h *Handler) revokeSession(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Auth.RevokeSession(r.Context(), user.ID, id); err != nil {
		return err
	}
	return h.settingsReply(w, r, user, pageData{}, kit.Failure{}, _accessCard)
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
	var form backupForm
	if err := h.app.DecodeForm(r, &form); err != nil {
		return err
	}
	pass, passphrase := form.Password, form.Passphrase
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
	var f deleteForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	// finish even if the browser gives up waiting, so the result does not depend on it
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	if f.Password == "" {
		errs := kit.FieldsError{"delete.password": domain.NewError(errors.ErrInvalid, "required")}
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: errs, Form: "delete"}, _deleteCard)
	}
	if err := h.app.Svc.Data.DeleteAll(ctx, user.ID, f.Password); err != nil {
		return h.settingsReply(w, r, user, pageData{}, kit.Failure{Err: err, Form: "delete"}, _deleteCard)
	}
	kit.Redirect(w, r, "/accounts")
	return nil
}
