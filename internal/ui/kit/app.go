package kit

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-playground/form/v4"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/logger"
	"github.com/ysomad/gigafin/internal/service"
)

// Cookies of the UI, named by App.Cookie.
const (
	SessionCookie = "gigafin_session"
	noticeCookie  = "gigafin_notice"
	flashCookie   = "gigafin_flash"
)

// App is what the handlers of every feature run with.
type App struct {
	Svc *service.Services
	Log *slog.Logger
	// Dicts translate the UI.
	Dicts i18n.Dicts
	// Decoder decodes forms and queries into structs; only fields with a form tag are set.
	Decoder       *form.Decoder
	SecureCookies bool
	SessionTTL    time.Duration
	MaxUpload     int64
}

// Handler serves a request of the signed-in user.
type Handler func(w http.ResponseWriter, r *http.Request, user domain.User) error

// Cookie returns a cookie with the site-wide attributes. With SecureCookies the name
// gets the __Host- prefix, so browsers refuse it unless Secure, host-only and on "/".
func (a *App) Cookie(name, value string, maxAge int) *http.Cookie {
	// Secure is off by default so the app works over plain http on localhost
	return &http.Cookie{ //nolint:gosec // see above
		Name:     a.cookieName(name),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// cookieName returns the name of cookie name as sent by the browser.
func (a *App) cookieName(name string) string {
	if a.SecureCookies {
		return "__Host-" + name
	}
	return name
}

// SessionToken returns the session token of the request, empty when it has none.
func (a *App) SessionToken(r *http.Request) string {
	return a.CookieValue(r, SessionCookie)
}

// CookieValue returns the value of cookie name, set with Cookie, empty when the request
// has none.
func (a *App) CookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(a.cookieName(name))
	if err != nil {
		return ""
	}
	return c.Value
}

// SetNotice sets msg to be shown as a success toast on the next page rendered, after a
// redirect.
func (a *App) SetNotice(w http.ResponseWriter, msg string) {
	http.SetCookie(w, a.Cookie(noticeCookie, url.QueryEscape(msg), 60))
}

// IsHTMX reports whether r is made by htmx.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// PathID returns the positive integer path value name, or errors.ErrNotFound.
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.ErrNotFound
	}
	return id, nil
}

// DecodeForm parses the posted form of r into v; only fields with a form tag are set.
// A form the UI never posts, such as one with a tampered id, is an invalid form error.
func (a *App) DecodeForm(r *http.Request, v any) error {
	if err := r.ParseForm(); err != nil {
		a.Log.DebugContext(r.Context(), "parse form", slog.Any("error", err))
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := a.Decoder.Decode(v, r.PostForm); err != nil {
		a.Log.DebugContext(r.Context(), "decode form", slog.Any("error", err))
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	return nil
}

// prefs returns the prefs of a request with settings, zero when signed out: the UI
// language is the one of the settings, else the browser's.
func (a *App) prefs(r *http.Request, settings domain.Settings) Prefs {
	p := Prefs{Settings: settings, Lang: settings.Language, Dicts: a.Dicts}
	if p.Lang == "" {
		p.Lang = i18n.Match(r.Header.Get("Accept-Language"))
	}
	return p
}

// Authed serves h to signed-in users with their Prefs in the request context, and
// redirects others to the login page.
func (a *App) Authed(h Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := a.Svc.Auth.SessionUser(r.Context(), a.SessionToken(r))
		if errors.Is(err, errors.ErrUnauthorized) {
			Redirect(w, r, "/login")
			return
		}
		if err != nil {
			a.ServerError(w, r, err)
			return
		}

		logger.WithAttrs(r.Context(), slog.Int64("user_id", user.ID))

		settings, err := a.Svc.Settings.Get(r.Context(), user.ID)
		if err != nil {
			a.ServerError(w, r, err)
			return
		}

		p := a.prefs(r, settings)
		p.Today = a.Svc.Today(user)
		r = r.WithContext(WithPrefs(r.Context(), p))

		r.Body = http.MaxBytesReader(w, r.Body, a.MaxUpload)
		if err := h(w, r, user); err != nil {
			a.handleError(w, r, user, err)
		}
	})
}

func (a *App) handleError(w http.ResponseWriter, r *http.Request, user domain.User, err error) {
	switch {
	case errors.Is(err, errors.ErrNotFound):
		err := Render(a, w, r, http.StatusNotFound, NotFound, Page{User: &user, Title: "Not found"},
			struct{}{}, Failure{})
		if err != nil {
			a.ServerError(w, r, err)
		}
	case IsUserError(err):
		// a forbidden request is one the UI never makes, so it may be tampering
		level := slog.LevelDebug
		if errors.Is(err, errors.ErrForbidden) {
			level = slog.LevelWarn
		}
		a.Log.Log(r.Context(), level, "request rejected", slog.Any("error", err))
		p := PrefsFrom(r.Context())
		msg := ErrText(a.Dicts, p.Lang, err)
		// an htmx action, one of no form to show the error on, leaves the page as it is
		// and gets the error as a toast
		if IsHTMX(r) {
			w.Header().Set("HX-Reswap", "none")
			toasts := Toasts(Page{Errors: &FormErrors{Toast: msg}}, true, nil)
			if err := a.Fragment(w, r, http.StatusUnprocessableEntity, toasts); err != nil {
				a.ServerError(w, r, err)
			}
			return
		}
		a.redirectBack(w, r, msg)
	default:
		a.ServerError(w, r, err)
	}
}

// IsUserError reports whether err is of a kind shown to the user on the page they
// came from.
func IsUserError(err error) bool {
	return errors.Is(err, errors.ErrInvalid) || errors.Is(err, errors.ErrExists) ||
		errors.Is(err, errors.ErrConflict) || errors.Is(err, errors.ErrUnauthorized) ||
		errors.Is(err, errors.ErrForbidden)
}

// ServerError logs err and replies with a 500.
func (a *App) ServerError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.ErrorContext(r.Context(), "request failed",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)

	msg := "Internal server error"
	if reqID := logger.RequestID(r.Context()); reqID != "" {
		msg = fmt.Sprintf("%s, request ID %s", msg, reqID)
	}

	http.Error(w, msg, http.StatusInternalServerError)
}

// Render writes page of v and d, with the errors of fail translated, or the flash
// message when fail is zero. Render is a func, not a method, for its type parameter.
func Render[T any](
	a *App,
	w http.ResponseWriter,
	r *http.Request,
	status int,
	page func(Page, T) templ.Component,
	v Page,
	d T,
	fail Failure,
) error {
	ctx := r.Context()
	p, ok := LookupPrefs(ctx)
	if !ok {
		// signed out: default settings, the browser language
		p = a.prefs(r, domain.Settings{})
		ctx = WithPrefs(ctx, p)
	}

	v.Errors = a.FormErrors(w, r.WithContext(ctx), fail)
	v.Notice = a.takeFlash(w, r, noticeCookie)
	v.Title = a.Dicts.T(p.Lang, v.Title, v.TitleArgs...)
	v.BankSync = v.User != nil && r.Method == http.MethodGet && !IsHTMX(r) && len(a.Svc.Banks.Enabled()) > 0

	var buf bytes.Buffer
	if err := page(v, d).Render(ctx, &buf); err != nil {
		return fmt.Errorf("render %s: %w", r.URL.Path, err)
	}
	// a field error no input shows, a key the handler and the template disagree on,
	// would leave the form failing without a word, so it is shown as a toast
	if unread := v.Errors.Unread(); len(unread) > 0 {
		a.Log.WarnContext(ctx, "field errors not shown", slog.Any("error", fail.Err))
		v.Errors.Toast = strings.Join(unread, "; ")
		buf.Reset()
		if err := page(v, d).Render(ctx, &buf); err != nil {
			return fmt.Errorf("render %s: %w", r.URL.Path, err)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	// a page rendered for a post, such as a form with errors, keeps the URL of the form,
	// since the action URL of a boosted form can't be reloaded
	if r.Method != http.MethodGet && IsHTMX(r) {
		w.Header().Set("HX-Push-Url", "false")
	}

	// and stays where it was instead of scrolling to the top, which htmx does for boosted
	// requests, so its errors or result are in view. A named form replaces just itself,
	// so the rest of the page keeps its state; forms with their own hx-post aren't
	// boosted and keep their own target.
	if r.Method != http.MethodGet && r.Header.Get("HX-Boosted") == "true" {
		if fail.Form != "" {
			sel := "#form-" + fail.Form
			w.Header().Set("HX-Retarget", sel)
			w.Header().Set("HX-Reselect", sel)
			w.Header().Set("HX-Reswap", "outerHTML show:none")
		} else {
			w.Header().Set("HX-Reswap", "innerHTML show:none")
		}
	}

	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		a.Log.DebugContext(r.Context(), "write response", slog.Any("error", err))
	}

	return nil
}

// FormErrors returns the errors of fail translated to the language of the request, or
// the flash message as a toast when fail is zero.
func (a *App) FormErrors(w http.ResponseWriter, r *http.Request, fail Failure) *FormErrors {
	p := PrefsFrom(r.Context())
	e := &FormErrors{FormName: fail.Form}
	switch fields, ok := errors.AsType[FieldsError](fail.Err); {
	case ok:
		e.Fields = make(map[string]string, len(fields))
		for k, err := range fields {
			e.Fields[k] = ErrText(a.Dicts, p.Lang, err)
		}
	case fail.Err != nil && fail.Form != "":
		e.Msg = ErrText(a.Dicts, p.Lang, fail.Err)
	case fail.Err != nil:
		e.Toast = ErrText(a.Dicts, p.Lang, fail.Err)
	default:
		e.Toast = a.takeFlash(w, r, flashCookie)
	}
	return e
}

// Fragment writes c alone, for htmx to swap in.
func (a *App) Fragment(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		return fmt.Errorf("render fragment %s: %w", r.URL.Path, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	if _, err := w.Write(buf.Bytes()); err != nil {
		a.Log.DebugContext(r.Context(), "write response", slog.Any("error", err))
	}

	return nil
}

// redirectBack returns to the referring page of this site with an error message. The
// message goes in a cookie, not the URL, so a crafted link cannot show arbitrary text
// on the site.
func (a *App) redirectBack(w http.ResponseWriter, r *http.Request, msg string) {
	target := "/"
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Path != "" && ref.Host == r.Host {
		target = ref.Path
		if ref.RawQuery != "" {
			target += "?" + ref.RawQuery
		}
	}
	http.SetCookie(w, a.Cookie(flashCookie, url.QueryEscape(msg), 60))
	Redirect(w, r, target)
}

// takeFlash returns the message of the flash cookie name, set by redirectBack for
// flashCookie or SetNotice for noticeCookie, and clears it.
func (a *App) takeFlash(w http.ResponseWriter, r *http.Request, name string) string {
	c, err := r.Cookie(a.cookieName(name))
	if err != nil {
		return ""
	}
	http.SetCookie(w, a.Cookie(name, "", -1))
	msg, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return msg
}

// Redirect sends the browser to a local path after a form post. htmx follows
// HX-Location with an ajax request, plain browsers get 303.
func Redirect(w http.ResponseWriter, r *http.Request, path string) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		path = "/"
	}
	if IsHTMX(r) {
		w.Header().Set("HX-Location", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Location", path)
	w.WriteHeader(http.StatusSeeOther)
}
