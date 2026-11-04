// Package service runs use cases: it loads domain models from repositories, applies
// domain rules and stores the result, in a transaction when a use case writes more than
// once. Handlers call services only.
//
// Helpers that take repository.Repos run inside the caller's transaction, so one use
// case can reuse another's steps, as an import does with accounts and operations.
package service

import (
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Services are all use cases over one store. Every finance method is scoped to one
// user.
type Services struct {
	Auth       *Auth
	Settings   *Settings
	Accounts   *Accounts
	Categories *Categories
	Operations *Operations
	Plans      *Plans
	Dashboard  *Dashboard
	Imports    *Imports
	Data       *Data
	Rates      *Rates
	Rules      *Rules

	clock timex.Clock
}

// Options tune use cases.
type Options struct {
	Session domain.SessionLifetime
	Import  ImportOptions
	// RateFetchers fetch exchange rates of each source users may pick.
	RateFetchers map[domain.RateSource]RateFetcher
}

// New returns services over store using clock for timestamps and today's date, logging
// business events to log. It fails on invalid options.
func New(store *repository.Store, clock timex.Clock, log *slog.Logger, opts Options) (*Services, error) {
	if err := opts.Session.Validate(); err != nil {
		return nil, err
	}
	imports, err := newImports(store, clock, log, opts.Import)
	if err != nil {
		return nil, err
	}
	rules := &Rules{store: store, clock: clock, log: log}
	imports.categorizer = rules
	auth := &Auth{store: store, clock: clock, log: log, session: opts.Session}
	return &Services{
		Auth:       auth,
		Settings:   &Settings{store: store, clock: clock, log: log},
		Accounts:   &Accounts{store: store, clock: clock, log: log},
		Categories: &Categories{store: store, clock: clock, log: log},
		Operations: &Operations{store: store, clock: clock, log: log, categorizer: rules},
		Plans:      &Plans{store: store, clock: clock, log: log},
		Dashboard:  &Dashboard{store: store},
		Imports:    imports,
		Data:       &Data{store: store, auth: auth, log: log},
		Rates:      &Rates{store: store, clock: clock, fetchers: opts.RateFetchers},
		Rules:      rules,
		clock:      clock,
	}, nil
}

// Today returns the current date in the user's time zone by the services' clock.
func (s *Services) Today(u domain.User) time.Time {
	return timex.Today(s.clock, u.Location())
}
