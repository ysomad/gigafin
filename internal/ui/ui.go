// Package ui serves the server-rendered htmx web interface.
package ui

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
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
	// _flashCookie carries an error message to the page a redirect lands on.
	_flashCookie = "gigafin_flash"
	_maxForm     = 1 << 20
)

var _pages = [...]string{
	"login", "dashboard", "operations", "operation_form", "accounts", "account", "categories", "category",
	"plans", "imports", "import", "import_new", "import_rates", "settings", "rules", "rule", "notfound",
}

// Config configures the UI.
type Config struct {
	Services *service.Services
	Log      *slog.Logger
	// SecureCookies sets the Secure flag and the __Host- prefix on the session cookie;
	// enable behind HTTPS.
	SecureCookies bool
	// SessionTTL is the max age of the session cookie, the server's absolute session
	// lifetime; the server ends idle sessions earlier.
	SessionTTL time.Duration
	// MaxUpload limits the body of a signed-in request, an import file included.
	MaxUpload int64
	// StaticCacheTTL is how long browsers cache static assets; 0 disables caching.
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
// settings redirects, so they are never stale when rendered.
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
	Error     error
	Data      any
}

// New parses templates and returns the UI.
func New(cfg Config) (*UI, error) {
	if cfg.SessionTTL <= 0 || cfg.MaxUpload <= 0 {
		return nil, fmt.Errorf("session ttl and max upload: must be positive: %w", errors.ErrInvalid)
	}
	u := &UI{
		svc:            cfg.Services,
		log:            cfg.Log,
		secureCookies:  cfg.SecureCookies,
		sessionTTL:     cfg.SessionTTL,
		maxUpload:      cfg.MaxUpload,
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
	frag, err := template.New("partials").Funcs(_funcs).Funcs(amountFuncs(domain.Settings{})).
		Funcs(langFuncs(nil, "en")).ParseFS(web.FS, "templates/partials.html")
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
		"POST /operations/{id}/split":         u.splitOperation,
		"GET /accounts":                       u.accounts,
		"POST /accounts":                      u.createAccount,
		"GET /accounts/{id}":                  u.account,
		"POST /accounts/{id}":                 u.updateAccount,
		"POST /accounts/{id}/balance":         u.setBalance,
		"POST /accounts/{id}/archive":         u.archiveAccount,
		"POST /accounts/{id}/delete":          u.deleteAccount,
		"GET /categories":                     u.categories,
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
		"POST /rules/learning":                u.setRuleLearning,
		"GET /rules/{id}":                     u.rule,
		"POST /rules/{id}":                    u.updateRule,
		"POST /rules/{id}/apply":              u.applyRule,
		"POST /rules/{id}/delete":             u.deleteRule,
		"GET /plans":                          u.plans,
		"POST /plans":                         u.setPlan,
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
		"POST /settings/timezone":             u.setTimezone,
		"POST /settings/display":              u.setDisplay,
		"POST /settings/language":             u.setLanguage,
		"POST /settings/rates":                u.setRates,
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
		Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true,
		Secure: u.secureCookies, SameSite: http.SameSiteLaxMode,
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
		u.redirectBack(w, r, errText(u.dicts, p.lang, err))
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
		slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Any("error", err))
	msg := "Internal error, see server logs."
	if id := logger.RequestID(r.Context()); id != "" {
		msg = "Internal error, see server logs for request ID " + id + "."
	}
	http.Error(w, msg, http.StatusInternalServerError)
}

func (u *UI) render(w http.ResponseWriter, r *http.Request, status int, page string, v view) error {
	if v.Error == nil {
		if msg := u.takeFlash(w, r); msg != "" {
			v.Error = errors.Error(msg)
		}
	}
	p, ok := r.Context().Value(prefsKey{}).(prefs)
	if !ok {
		// signed out: default settings, the browser language
		p.lang = i18n.Match(r.Header.Get("Accept-Language"))
	}
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
	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		u.log.DebugContext(r.Context(), "write response", slog.Any("error", err))
	}
	return nil
}

func (u *UI) fragment(w http.ResponseWriter, r *http.Request, name string, data any) error {
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

// takeFlash returns the message set by redirectBack and clears it.
func (u *UI) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(u.cookie(_flashCookie, "", 0).Name)
	if err != nil {
		return ""
	}
	http.SetCookie(w, u.cookie(_flashCookie, "", -1))
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
