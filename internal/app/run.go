package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logger"
)

// _shutdownTimeout is how long the server waits for requests on shutdown. It outlasts
// the work requests finish after the client is gone, the 2-minute bank sync the
// longest, so the database is not closed under them.
const _shutdownTimeout = 3 * time.Minute

// Run runs the HTTP server and the background jobs until SIGINT/SIGTERM arrives.
func Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(logger.Handler{Handler: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     cfg.Logger.Level,
		AddSource: cfg.Logger.AddSource,
	})})
	slog.SetDefault(log)

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

	svc, closeStore, err := NewServices(ctx, cfg, log)
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
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _shutdownTimeout)
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
