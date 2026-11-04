// Package app wires the dependencies of gigafin and runs its commands.
package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logx"
)

// Run runs the command in args until it finishes or SIGINT/SIGTERM arrives.
func Run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	serving := len(args) == 1 && args[0] == "serve"
	// the user commands print their result to stdout, so they log to stderr
	var out io.Writer = os.Stderr
	if serving {
		out = os.Stdout
	}
	log := slog.New(logx.Handler{Handler: slog.NewJSONHandler(out, &slog.HandlerOptions{
		Level:     cfg.Logger.Level,
		AddSource: cfg.Logger.AddSource,
	})})
	slog.SetDefault(log)

	switch {
	case serving:
		return serve(ctx, cfg, log)
	case len(args) >= 2 && args[0] == "user" && args[1] == "add":
		return userAdd(ctx, cfg, log, args[2:])
	case len(args) >= 2 && args[0] == "user" && args[1] == "passwd":
		return userPasswd(ctx, cfg, log, args[2:])
	case len(args) >= 2 && args[0] == "user" && args[1] == "role":
		return userRole(ctx, cfg, log, args[2:])
	default:
		const usage errors.Error = `usage:
  gigafin serve
  gigafin user add    -login LOGIN [-role user|admin] [-tz ZONE]
  gigafin user passwd -login LOGIN
  gigafin user role   -login LOGIN -role user|admin
settings come from the environment: DB_PATH, LOGGER_LEVEL, HTTP_ADDR, HTTP_SECURE_COOKIES, ...`
		return usage
	}
}
