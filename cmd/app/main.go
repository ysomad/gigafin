// Command app serves gigafin, a personal finance manager: the web UI, the gigafin API
// and the background jobs.
//
// Settings come from the environment, see internal/config.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ysomad/gigafin/internal/app"
	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/logger"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin:", err)
		os.Exit(1)
	}
}

// run serves until SIGINT/SIGTERM arrives.
func run() error {
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

	return app.Serve(ctx, cfg, log)
}
