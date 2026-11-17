// Package ui serves the server-rendered htmx web interface. Each feature is a package
// of its handlers and templates; kit holds what they share.
package ui

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-playground/form/v4"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/ui/accounts"
	"github.com/ysomad/gigafin/internal/ui/analytics"
	"github.com/ysomad/gigafin/internal/ui/auth"
	"github.com/ysomad/gigafin/internal/ui/categories"
	"github.com/ysomad/gigafin/internal/ui/imports"
	"github.com/ysomad/gigafin/internal/ui/kit"
	"github.com/ysomad/gigafin/internal/ui/operations"
	"github.com/ysomad/gigafin/internal/ui/plans"
	"github.com/ysomad/gigafin/internal/ui/rules"
	"github.com/ysomad/gigafin/internal/ui/settings"
	"github.com/ysomad/gigafin/web"
)

// Config configures the UI.
type Config struct {
	Services       *service.Services
	Log            *slog.Logger
	SecureCookies  bool
	SessionTTL     time.Duration
	MaxUploadSize  int64
	StaticCacheTTL time.Duration
}

// UI holds handlers.
type UI struct {
	app            *kit.App
	staticCacheTTL time.Duration
}

// New returns the UI.
func New(cfg Config) (*UI, error) {
	if cfg.SessionTTL <= 0 || cfg.MaxUploadSize <= 0 {
		return nil, fmt.Errorf("session ttl and max upload: must be positive: %w", errors.ErrInvalid)
	}

	dicts, err := i18n.Load(web.FS)
	if err != nil {
		return nil, fmt.Errorf("load locales: %w", err)
	}

	decoder := form.NewDecoder()
	decoder.SetMode(form.ModeExplicit)

	return &UI{
		app: &kit.App{
			Svc:           cfg.Services,
			Log:           cfg.Log,
			Dicts:         dicts,
			Decoder:       decoder,
			SecureCookies: cfg.SecureCookies,
			SessionTTL:    cfg.SessionTTL,
			MaxUpload:     cfg.MaxUploadSize,
		},
		staticCacheTTL: cfg.StaticCacheTTL,
	}, nil
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

	auth.New(u.app).Register(mux)
	mux.HandleFunc("GET /noop", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	features := []interface{ Routes() map[string]kit.Handler }{
		analytics.New(u.app),
		operations.New(u.app),
		accounts.New(u.app),
		categories.New(u.app),
		rules.New(u.app),
		plans.New(u.app),
		imports.New(u.app),
		settings.New(u.app),
	}
	for _, f := range features {
		for pattern, h := range f.Routes() {
			mux.Handle(pattern, u.app.Authed(h))
		}
	}
	mux.Handle("/", u.app.Authed(func(http.ResponseWriter, *http.Request, domain.User) error {
		return errors.ErrNotFound
	}))
	return nil
}
