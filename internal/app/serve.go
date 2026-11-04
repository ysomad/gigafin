package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
)

// serve runs the HTTP server and the background jobs until ctx is done.
func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	version := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				version = s.Value
			}
		}
	}
	log.InfoContext(ctx, "starting", slog.String("version", version), slog.String("db_path", cfg.DBPath),
		slog.String("log_level", cfg.Logger.Level.String()))
	log.DebugContext(ctx, "config", slog.Any("config", cfg))

	svc, closeStore, err := newService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()

	sched, err := newScheduler(svc, log)
	if err != nil {
		return err
	}
	// deferred after closeStore, so jobs stop before the database closes
	defer func() {
		if err := sched.Shutdown(); err != nil {
			log.ErrorContext(ctx, "shutdown scheduler", slog.Any("error", err))
		}
	}()

	srv, err := newServer(cfg, svc, log)
	if err != nil {
		return err
	}

	sched.Start()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.InfoContext(ctx, "listening", slog.String("addr", cfg.HTTP.Addr))

	select {
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}

	log.InfoContext(ctx, "shutting down")
	start := time.Now()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	log.InfoContext(ctx, "stopped", slog.Int64("duration_ms", time.Since(start).Milliseconds()))
	return nil
}
