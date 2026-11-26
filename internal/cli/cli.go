// Package cli is the command line of gigafin: it serves the app and manages its users.
//
// Usage:
//
//	gigafin serve
//	gigafin user add -login alice [-role admin]
//	gigafin user passwd -login alice [-revoke-keys]
//	gigafin user role -login alice -role user
//	gigafin user 2fa-off -login alice
//
// Settings come from the environment, see internal/config.
package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ysomad/gigafin/internal/app"
	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logger"
)

// Run runs the command in args until it finishes or SIGINT/SIGTERM arrives.
func Run(args []string) error {
	// the server sets up its own logger and signal handling
	if len(args) == 1 && args[0] == "serve" {
		return app.Run()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// the commands print their result to stdout, so they log to stderr
	log := slog.New(logger.Handler{Handler: slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     cfg.Logger.Level,
		AddSource: cfg.Logger.AddSource,
	})})
	slog.SetDefault(log)

	switch {
	case len(args) >= 2 && args[0] == "user" && args[1] == "add":
		return userAdd(ctx, cfg, log, args[2:])
	case len(args) >= 2 && args[0] == "user" && args[1] == "passwd":
		return userPasswd(ctx, cfg, log, args[2:])
	case len(args) >= 2 && args[0] == "user" && args[1] == "role":
		return userRole(ctx, cfg, log, args[2:])
	case len(args) >= 2 && args[0] == "user" && args[1] == "2fa-off":
		return userTwoFactorOff(ctx, cfg, log, args[2:])
	default:
		const usage errors.Error = `usage:
  gigafin serve
  gigafin user add     -login LOGIN [-role user|admin] [-tz ZONE]
  gigafin user passwd  -login LOGIN [-revoke-keys]
  gigafin user role    -login LOGIN -role user|admin
  gigafin user 2fa-off -login LOGIN
settings come from the environment: DB_PATH, LOGGER_LEVEL, ...`
		return usage
	}
}
