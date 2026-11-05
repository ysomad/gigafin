package app

import (
	"crypto/rand"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	apispec "github.com/ysomad/gigafin/api"
	"github.com/ysomad/gigafin/internal/api"
	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logger"
	"github.com/ysomad/gigafin/internal/mcp"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/ui"
)

// newServer returns the HTTP server of the web UI, the public API with its docs and
// the MCP endpoint.
func newServer(cfg config.Config, svc *service.Services, log *slog.Logger) (*http.Server, error) {
	if cfg.HTTP.APIDocs && !apispec.DocsEmbedded {
		return nil, fmt.Errorf("api docs: binary built without the apidocs tag: %w", errors.ErrInvalid)
	}
	web, err := ui.New(ui.Config{
		Services: svc, Log: log, SecureCookies: cfg.HTTP.SecureCookies,
		SessionTTL: cfg.Session.TTL, MaxUpload: cfg.HTTP.MaxUploadBytes, StaticCacheTTL: cfg.HTTP.StaticCacheTTL,
	})
	if err != nil {
		return nil, err
	}

	apiHandler, err := api.New(svc, log)
	if err != nil {
		return nil, fmt.Errorf("create api: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle(api.PathPrefix+"/", apiHandler)
	mux.HandleFunc("GET "+api.PathPrefix+"/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, apispec.FS, "public.yaml")
	})
	if cfg.HTTP.APIDocs {
		docs, err := fs.Sub(apispec.DocsFS, "docs")
		if err != nil {
			return nil, fmt.Errorf("api docs fs: %w", err)
		}
		mux.HandleFunc("GET "+api.PathPrefix+"/docs", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, docs, "docs.html")
		})
		mux.Handle("GET "+api.PathPrefix+"/docs/", http.StripPrefix(api.PathPrefix+"/docs/", http.FileServerFS(docs)))
	}

	spec, err := apispec.FS.ReadFile("public.yaml")
	if err != nil {
		return nil, fmt.Errorf("read api spec: %w", err)
	}

	mcpHandler, err := mcp.New(svc.Auth, apiHandler, spec, log)
	if err != nil {
		return nil, fmt.Errorf("create mcp: %w", err)
	}
	mux.Handle(mcp.Path, mcpHandler)
	if err := web.Register(mux); err != nil {
		return nil, err
	}

	// timeouts are the HashiCorp Vault listener defaults; no write timeout, as request
	// bodies are bounded by the handlers instead
	return &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           logRequests(protect(mux, cfg.HTTP.SecureCookies), log),
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       5 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}, nil
}

// protect wraps the whole server: security headers and cross-origin protection,
// which replaces CSRF tokens. https adds HSTS, it is only honored over HTTPS.
func protect(next http.Handler, https bool) http.Handler {
	protected := http.NewCrossOriginProtection().Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if https {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		protected.ServeHTTP(w, r)
	})
}

// logRequests wraps the whole server: it gives every request an ID, returned in the
// X-Request-Id header and added to every log record of the request, recovers panics
// and logs one line per request once it is answered. Static assets log at debug.
func logRequests(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := rand.Text()
		ctx := logger.Context(r.Context(), id)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-Id", id)
		rw := &responseWriter{ResponseWriter: w}

		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler { //nolint:errorlint // re-panicked by net/http, never wrapped
					panic(p)
				}
				log.ErrorContext(ctx, "panic", slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
				if rw.status == 0 {
					http.Error(rw, "Internal error, request ID "+id+".", http.StatusInternalServerError)
				}
			}
			level := slog.LevelInfo
			if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/noop" {
				level = slog.LevelDebug
			}
			log.LogAttrs(ctx, level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int64("bytes", rw.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote_addr", r.RemoteAddr),
			)
		}()
		next.ServeHTTP(rw, r)
		if rw.status == 0 {
			rw.status = http.StatusOK
		}
	})
}

// responseWriter records the status and size of a response.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the flusher and deadlines of the
// underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConnsPerHost:   runtime.GOMAXPROCS(0) + 1,
		},
	}
}
