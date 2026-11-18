// Command cli manages the users of gigafin.
//
// Usage:
//
//	gigafin-cli user add -login alice [-role admin]
//	gigafin-cli user passwd -login alice
//	gigafin-cli user role -login alice -role user
//	gigafin-cli user 2fa-off -login alice
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

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logger"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gigafin-cli:", err)
		os.Exit(1)
	}
}

// run runs the command in args until it finishes or SIGINT/SIGTERM arrives.
func run(args []string) error {
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
  gigafin-cli user add     -login LOGIN [-role user|admin] [-tz ZONE]
  gigafin-cli user passwd  -login LOGIN
  gigafin-cli user role    -login LOGIN -role user|admin
  gigafin-cli user 2fa-off -login LOGIN
settings come from the environment: DB_PATH, LOGGER_LEVEL, ...`
		return usage
	}
}
