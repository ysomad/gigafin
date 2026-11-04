package app

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/service"
)

func userAdd(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	// the IANA name of the local zone: $TZ, or the zoneinfo file /etc/localtime links to
	local := os.Getenv("TZ")
	if local == "" {
		if link, err := os.Readlink("/etc/localtime"); err == nil {
			if _, name, ok := strings.Cut(link, "zoneinfo/"); ok {
				local = name
			}
		}
	}
	if local == "" {
		local = "UTC"
	}
	tz := fs.String("tz", local, "IANA time zone of the user")
	role := fs.String("role", string(domain.RoleUser), "role of the user: user or admin")
	return userCommand(ctx, cfg, log, fs, args, "Password: ", func(a *service.Auth, login, pass string) error {
		u, err := a.CreateUser(ctx, login, pass, *tz, *role)
		if err != nil {
			return err
		}
		fmt.Printf("created %s %q (id %d)\n", u.Role, u.Login, u.ID)
		return nil
	})
}

func userRole(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("user role", flag.ContinueOnError)
	login := fs.String("login", "", "login of the user")
	role := fs.String("role", "", "new role of the user: user or admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *login == "" {
		return errors.Error("-login is required")
	}
	svc, closeStore, err := newService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()
	if err := svc.Auth.SetRoleByLogin(ctx, *login, *role); err != nil {
		return err
	}
	fmt.Printf("role of %q set to %s\n", *login, *role)
	return nil
}

func userPasswd(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("user passwd", flag.ContinueOnError)
	return userCommand(ctx, cfg, log, fs, args, "New password: ", func(a *service.Auth, login, pass string) error {
		if err := a.SetPasswordByLogin(ctx, login, pass); err != nil {
			return err
		}
		fmt.Printf("password of %q changed, sessions ended\n", login)
		return nil
	})
}

// userCommand parses -login and -p on top of fs, reads the password unless given and
// runs fn.
func userCommand(
	ctx context.Context, cfg config.Config, log *slog.Logger, fs *flag.FlagSet, args []string, prompt string,
	fn func(a *service.Auth, login, pass string) error,
) error {
	login := fs.String("login", "", "login of the user")
	pass := fs.String("p", "", "password of the user; prompted for when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *login == "" {
		return errors.Error("-login is required")
	}
	if *pass == "" {
		p, err := readPassword(prompt)
		if err != nil {
			return err
		}
		*pass = p
	}
	svc, closeStore, err := newService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeStore()
	return fn(svc.Auth, *login, *pass)
}

// readPassword prompts on a terminal without echo, twice, or reads one line from a
// pipe.
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	read := func(p string) (string, error) {
		fmt.Fprint(os.Stderr, p)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}
	p1, err := read(prompt)
	if err != nil {
		return "", err
	}
	p2, err := read("Repeat: ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.Error("passwords do not match")
	}
	return p1, nil
}
