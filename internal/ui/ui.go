// Package ui serves the server-rendered htmx web interface.
package ui

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/form/v4"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/logger"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/web"
)

const (
	_sessionCookie = "gigafin_session"
	_flashCookie   = "gigafin_flash"
	_noticeCookie  = "gigafin_notice"
	_maxForm       = 1 << 20
)

var _pages = [...]string{
	"login", "dashboard", "operations", "operation_form", "operation_split", "accounts", "account", "account_new",
	"categories", "category", "category_new", "plans", "plan_new", "imports", "import", "import_new", "import_rates",
	"settings", "rules", "rule", "notfound",
}

// Config configures the UI.
type Config struct {
	Services       *service.Services
	Log            *slog.Logger
	SecureCookies  bool
	SessionTTL     time.Duration
	MaxUploadSize  int64
	StaticCacheTTL time.Duration
}

// UI holds handlers and parsed templates.
type UI struct {
	svc            *service.Services
	log            *slog.Logger
	secureCookies  bool
	sessionTTL     time.Duration
	maxUpload      int64
	staticCacheTTL time.Duration
	dicts          i18n.Dicts
	pages          map[string]*template.Template
	fragments      *template.Template
	// formDecoder decodes forms and queries into structs; only fields with a form tag are set.
	formDecoder *form.Decoder
}

type handler func(w http.ResponseWriter, r *http.Request, user domain.User) error

// prefs are the settings and UI language of the signed-in user, read once per request
// by authed and kept in the request context under prefsKey. A handler that changes the
// settings redirects, so they are never stale when rendered, except the settings page
// rendered for htmx, which reads the settings itself and shows no amounts.
type prefs struct {
	settings domain.Settings
	lang     string
}

type prefsKey struct{}

// view is the data of every page.
type view struct {
	User *domain.User
	Nav  string
	// Title is translated, then formatted with TitleArgs, if any.
	Title     string
	TitleArgs []any
	// Error is the error of a failed form post: fieldsError of its inputs, or an error
	// of the business logic. render turns it, or the flash message of redirectBack, into
	// Errors.
	Error error
	// Form names the posted form, which shows an Error other than fieldsError on itself
	// rather than as a toast; see formErrors.Form.
	Form   string
	Errors *formErrors
	// Notice is a success message set before a redirect, shown as a toast.
	Notice string
	Data   any
}

// fieldsError holds the translated messages of the inputs of a form that failed
// validation, keyed by input name. A handler validates every input of its form, before
// calling a service, and passes them as the view error.
type fieldsError map[string]string

// type check
var _ error = fieldsError{}

// Error implements the error interface for fieldsError.
func (e fieldsError) Error() string {
	msgs := make([]string, 0, len(e))
	for _, k := range slices.Sorted(maps.Keys(e)) {
		msgs = append(msgs, k+": "+e[k])
	}
	return strings.Join(msgs, "; ")
}

// formErrors are the translated messages of an error shown on a page: field messages,
// shown next to their inputs, a form message, shown on the posted form, and a toast.
type formErrors struct {
	fields fieldsError
	// form is the form of msg.
	form  string
	msg   string
	toast string
}

// Get returns the message about the input named field, if any.
func (e *formErrors) Get(field string) string {
	if e == nil {
		return ""
	}
	return e.fields[field]
}

// Form returns the message about the form named name, if any.
func (e *formErrors) Form(name string) string {
	if e == nil || e.form != name {
		return ""
	}
	return e.msg
}

// Toasts returns the messages shown as toasts.
func (e *formErrors) Toasts() []string {
	if e == nil || e.toast == "" {
		return nil
	}
	return []string{e.toast}
}

// New parses templates and returns the UI.
func New(cfg Config) (*UI, error) {
	if cfg.SessionTTL <= 0 || cfg.MaxUploadSize <= 0 {
		return nil, fmt.Errorf("session ttl and max upload: must be positive: %w", errors.ErrInvalid)
	}

	u := &UI{
		svc:            cfg.Services,
		log:            cfg.Log,
		secureCookies:  cfg.SecureCookies,
		sessionTTL:     cfg.SessionTTL,
		maxUpload:      cfg.MaxUploadSize,
		staticCacheTTL: cfg.StaticCacheTTL,
		pages:          make(map[string]*template.Template, len(_pages)),
		formDecoder:    form.NewDecoder(),
	}

	u.formDecoder.SetMode(form.ModeExplicit)

	dicts, err := i18n.Load(web.FS)
	if err != nil {
		return nil, fmt.Errorf("load locales: %w", err)
	}

	u.dicts = dicts
	for _, p := range _pages {
		t, err := template.New(p).Funcs(_funcs).Funcs(amountFuncs(domain.Settings{})).
			Funcs(langFuncs(nil, "en")).ParseFS(web.FS,
			"templates/layout.html", "templates/partials.html", "templates/"+p+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}

		u.pages[p] = t
	}

	frag, err := template.
		New("partials").
		Funcs(_funcs).
		Funcs(amountFuncs(domain.Settings{})).
		Funcs(langFuncs(nil, "en")).
		ParseFS(web.FS, "templates/partials.html")
	if err != nil {
		return nil, fmt.Errorf("parse partials: %w", err)
	}

	u.fragments = frag
	return u, nil
}

// Register adds UI routes to mux.
func (u *UI) Register(mux *http.ServeMux) error {
	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}

	fileServer := http.StripPrefix("/static/", http.FileServerFS(static))

	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u.staticCacheTTL > 0 {
			w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(u.staticCacheTTL.Seconds())))
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}

		fileServer.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /login", u.loginPage)
	mux.HandleFunc("POST /login", u.login)
	mux.HandleFunc("POST /logout", u.logout)
	mux.HandleFunc("GET /noop", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	routes := map[string]handler{
		"GET /{$}":                            u.dashboard,
		"GET /operations":                     u.operations,
		"GET /operations/new":                 u.newOperation,
		"GET /operations/split":               u.operationSplit,
		"POST /operations":                    u.createOperation,
		"POST /operations/fill":               u.fillOperation,
		"POST /operations/rules":              u.categorizeOperations,
		"GET /operations/{id}/edit":           u.editOperation,
		"POST /operations/{id}":               u.updateOperation,
		"POST /operations/{id}/delete":        u.deleteOperation,
		"GET /operations/{id}/split":          u.splitOperationPage,
		"POST /operations/{id}/split":         u.splitOperation,
		"GET /accounts":                       u.accounts,
		"POST /accounts":                      u.createAccount,
		"GET /accounts/new":                   u.newAccount,
		"GET /accounts/{id}":                  u.account,
		"POST /accounts/{id}":                 u.updateAccount,
		"POST /accounts/{id}/balance":         u.setBalance,
		"POST /accounts/{id}/archive":         u.archiveAccount,
		"POST /accounts/{id}/delete":          u.deleteAccount,
		"GET /categories":                     u.categories,
		"GET /categories/new":                 u.newCategory,
		"POST /categories":                    u.createCategory,
		"GET /categories/{id}":                u.category,
		"POST /categories/{id}":               u.updateCategory,
		"POST /categories/{id}/archive":       u.archiveCategory,
		"POST /categories/{id}/delete":        u.deleteCategory,
		"GET /rules":                          u.rules,
		"GET /rules/new":                      u.newRule,
		"POST /rules":                         u.createRule,
		"GET /rules/condition":                u.ruleCondition,
		"GET /rules/action":                   u.ruleAction,
		"POST /rules/preview":                 u.previewRule,
		"GET /rules/{id}":                     u.rule,
		"POST /rules/{id}":                    u.updateRule,
		"POST /rules/{id}/apply":              u.applyRule,
		"POST /rules/{id}/delete":             u.deleteRule,
		"GET /plans":                          u.plans,
		"POST /plans":                         u.setPlan,
		"GET /plans/new":                      u.newPlan,
		"POST /plans/fill":                    u.fillPlans,
		"GET /imports":                        u.imports,
		"POST /imports":                       u.upload,
		"POST /imports/rates":                 u.previewRates,
		"POST /imports/rates/commit":          u.commitRates,
		"GET /imports/new":                    u.importNew,
		"GET /imports/{id}":                   u.importPage,
		"GET /imports/{id}/{step}":            u.importPage,
		"POST /imports/{id}/accounts":         u.saveAccounts,
		"POST /imports/{id}/categories":       u.saveCategories,
		"POST /imports/{id}/unpair/{row}":     u.unpair,
		"POST /imports/{id}/commit":           u.commit,
		"POST /imports/{id}/undo":             u.undo,
		"POST /imports/{id}/discard":          u.discard,
		"GET /settings":                       u.settings,
		"POST /settings":                      u.saveSettings,
		"POST /settings/rates/sync":           u.syncRates,
		"POST /settings/rates/set":            u.setRate,
		"POST /settings/rates/reset":          u.resetRate,
		"POST /settings/password":             u.changePassword,
		"POST /settings/api-keys":             u.createAPIKey,
		"POST /settings/delete-data":          u.deleteAllData,
		"GET /settings/export":                u.exportData,
		"POST /settings/backup":               u.backup,
		"POST /settings/api-keys/{id}/revoke": u.revokeAPIKey,
		"POST /settings/sessions/{id}/revoke": u.revokeSession,
	}
	for pattern, h := range routes {
		mux.Handle(pattern, u.authed(h))
	}
	mux.Handle("/", u.authed(func(w http.ResponseWriter, r *http.Request, user domain.User) error {
		return u.render(w, r, http.StatusNotFound, "notfound", view{User: &user, Title: "Not found"})
	}))
	return nil
}

// cookie returns a cookie with the site-wide attributes. With Config.SecureCookies
// the name gets the __Host- prefix, so browsers refuse it unless Secure, host-only
// and on "/".
func (u *UI) cookie(name, value string, maxAge int) *http.Cookie {
	if u.secureCookies {
		name = "__Host-" + name
	}
	// Secure is off by default so the app works over plain http on localhost
	return &http.Cookie{ //nolint:gosec // see above
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   u.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

func (u *UI) authed(h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var token string
		if c, err := r.Cookie(u.cookie(_sessionCookie, "", 0).Name); err == nil {
			token = c.Value
		}
		user, err := u.svc.Auth.SessionUser(r.Context(), token)
		if errors.Is(err, errors.ErrUnauthorized) {
			redirect(w, r, "/login")
			return
		}
		if err != nil {
			u.serverError(w, r, err)
			return
		}
		logger.WithAttrs(r.Context(), slog.Int64("user_id", user.ID))
		settings, err := u.svc.Settings.Get(r.Context(), user.ID)
		if err != nil {
			u.serverError(w, r, err)
			return
		}
		p := prefs{settings: settings, lang: settings.Language}
		if p.lang == "" {
			p.lang = i18n.Match(r.Header.Get("Accept-Language"))
		}
		r = r.WithContext(context.WithValue(r.Context(), prefsKey{}, p))
		r.Body = http.MaxBytesReader(w, r.Body, u.maxUpload)
		if err := h(w, r, user); err != nil {
			u.handleError(w, r, user, err)
		}
	})
}

func (u *UI) handleError(w http.ResponseWriter, r *http.Request, user domain.User, err error) {
	switch {
	case errors.Is(err, errors.ErrNotFound):
		//nolint:errcheck // render logs its own errors
		_ = u.render(w, r, http.StatusNotFound, "notfound", view{User: &user, Title: "Not found"})
	case isUserError(err):
		// a forbidden request is one the UI never makes, so it may be tampering
		level := slog.LevelDebug
		if errors.Is(err, errors.ErrForbidden) {
			level = slog.LevelWarn
		}
		u.log.Log(r.Context(), level, "request rejected", slog.Any("error", err))
		p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
		msg := errText(u.dicts, p.lang, err)
		// an htmx action, one of no form to show the error on, leaves the page as it is
		// and gets the error as a toast
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Reswap", "none")
			//nolint:errcheck // fragment logs its own errors
			_ = u.fragment(w, r, http.StatusUnprocessableEntity, "toast", msg)
			return
		}
		u.redirectBack(w, r, msg)
	default:
		u.serverError(w, r, err)
	}
}

// isUserError reports whether err is of a kind shown to the user on the page they
// came from.
func isUserError(err error) bool {
	return errors.Is(err, errors.ErrInvalid) || errors.Is(err, errors.ErrExists) ||
		errors.Is(err, errors.ErrConflict) || errors.Is(err, errors.ErrUnauthorized) ||
		errors.Is(err, errors.ErrForbidden)
}

func (u *UI) serverError(w http.ResponseWriter, r *http.Request, err error) {
	u.log.ErrorContext(r.Context(), "request failed",
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

func (u *UI) render(w http.ResponseWriter, r *http.Request, status int, page string, v view) error {
	p, ok := r.Context().Value(prefsKey{}).(prefs)
	if !ok {
		// signed out: default settings, the browser language
		p.lang = i18n.Match(r.Header.Get("Accept-Language"))
	}
	switch fields, ok := errors.AsType[fieldsError](v.Error); {
	case ok:
		v.Errors = &formErrors{fields: fields}
	case v.Error != nil && v.Form != "":
		v.Errors = &formErrors{form: v.Form, msg: errText(u.dicts, p.lang, v.Error)}
	case v.Error != nil:
		v.Errors = &formErrors{toast: errText(u.dicts, p.lang, v.Error)}
	default:
		v.Errors = &formErrors{toast: u.takeFlash(w, r, _flashCookie)}
	}
	v.Notice = u.takeFlash(w, r, _noticeCookie)
	v.Title = u.dicts.T(p.lang, v.Title, v.TitleArgs...)
	// a clone, since funcs of an executed template can no longer change
	t, err := u.pages[page].Clone()
	if err != nil {
		u.serverError(w, r, fmt.Errorf("clone %s: %w", page, err))
		return nil
	}
	var buf bytes.Buffer
	if err := t.Funcs(amountFuncs(p.settings)).
		Funcs(langFuncs(u.dicts, p.lang)).
		ExecuteTemplate(&buf, "layout", v); err != nil {
		u.serverError(w, r, fmt.Errorf("render %s: %w", page, err))
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// a page rendered for a post, such as a form with errors, keeps the URL of the form,
	// since the action URL of a boosted form can't be reloaded
	if r.Method != http.MethodGet && r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Push-Url", "false")
	}
	// and stays where it was instead of scrolling to the top, which htmx does for boosted
	// requests, so its errors or result are in view. A named form replaces just itself,
	// so the rest of the page keeps its state; forms with their own hx-post aren't
	// boosted and keep their own target.
	if r.Method != http.MethodGet && r.Header.Get("HX-Boosted") == "true" {
		if v.Form != "" {
			sel := "#form-" + v.Form
			w.Header().Set("HX-Retarget", sel)
			w.Header().Set("HX-Reselect", sel)
			w.Header().Set("HX-Reswap", "outerHTML show:none")
		} else {
			w.Header().Set("HX-Reswap", "innerHTML show:none")
		}
	}
	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		u.log.DebugContext(r.Context(), "write response", slog.Any("error", err))
	}
	return nil
}

func (u *UI) fragment(w http.ResponseWriter, r *http.Request, status int, name string, data any) error {
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	t, err := u.fragments.Clone()
	if err != nil {
		u.serverError(w, r, fmt.Errorf("clone fragments: %w", err))
		return nil
	}
	var buf bytes.Buffer
	if err := t.Funcs(amountFuncs(p.settings)).
		Funcs(langFuncs(u.dicts, p.lang)).
		ExecuteTemplate(&buf, name, data); err != nil {
		u.serverError(w, r, fmt.Errorf("render %s: %w", name, err))
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		u.log.DebugContext(r.Context(), "write response", slog.Any("error", err))
	}
	return nil
}

// redirectBack returns to the referring page of this site with an error message. The
// message goes in a cookie, not the URL, so a crafted link cannot show arbitrary text
// on the site.
func (u *UI) redirectBack(w http.ResponseWriter, r *http.Request, msg string) {
	target := "/"
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Path != "" && ref.Host == r.Host {
		target = ref.Path
		if ref.RawQuery != "" {
			target += "?" + ref.RawQuery
		}
	}
	http.SetCookie(w, u.cookie(_flashCookie, url.QueryEscape(msg), 60))
	redirect(w, r, target)
}

// takeFlash returns the message of the flash cookie name, set by redirectBack for
// _flashCookie, and clears it.
func (u *UI) takeFlash(w http.ResponseWriter, r *http.Request, name string) string {
	c, err := r.Cookie(u.cookie(name, "", 0).Name)
	if err != nil {
		return ""
	}
	http.SetCookie(w, u.cookie(name, "", -1))
	msg, err := url.QueryUnescape(c.Value)
	if err != nil {
		return ""
	}
	return msg
}

// redirect sends the browser to a local path after a form post. htmx follows
// HX-Location with an ajax request, plain browsers get 303.
func redirect(w http.ResponseWriter, r *http.Request, path string) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		path = "/"
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Location", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Location", path)
	w.WriteHeader(http.StatusSeeOther)
}
