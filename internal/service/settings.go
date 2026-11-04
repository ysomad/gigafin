package service

import (
	"context"
	"log/slog"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Settings manages user settings.
type Settings struct {
	store *repository.Store
	clock timex.Clock
	log   *slog.Logger
}

// Get returns the user's settings.
func (s *Settings) Get(ctx context.Context, userID int64) (domain.Settings, error) {
	return s.store.Read().Settings.Get(ctx, userID)
}

// SetDisplay stores the user's display preferences, keeping the other settings.
func (s *Settings) SetDisplay(ctx context.Context, userID int64, showMinorUnits bool) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		settings, err := r.Settings.Get(ctx, userID)
		if err != nil {
			return err
		}
		settings.ShowMinorUnits = showMinorUnits
		return r.Settings.Set(ctx, userID, settings, s.clock.Now())
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "display settings set", slog.Int64("user_id", userID),
		slog.Bool("show_minor_units", showMinorUnits))
	return nil
}

// SetRates stores where the user's exchange rates come from and the currency totals are
// converted to, keeping the other settings.
func (s *Settings) SetRates(ctx context.Context, userID int64, source domain.RateSource, primaryCurrency string) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		settings, err := r.Settings.Get(ctx, userID)
		if err != nil {
			return err
		}
		settings.RateSource, settings.PrimaryCurrency = source, primaryCurrency
		if err := settings.Validate(); err != nil {
			return err
		}
		return r.Settings.Set(ctx, userID, settings, s.clock.Now())
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "rate settings set", slog.Int64("user_id", userID),
		slog.String("rate_source", string(source)), slog.String("primary_currency", primaryCurrency))
	return nil
}

// SetLanguage stores the user's UI language, empty to follow the browser, keeping the
// other settings.
func (s *Settings) SetLanguage(ctx context.Context, userID int64, lang string) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		settings, err := r.Settings.Get(ctx, userID)
		if err != nil {
			return err
		}
		settings.Language = lang
		if err := settings.Validate(); err != nil {
			return err
		}
		return r.Settings.Set(ctx, userID, settings, s.clock.Now())
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "language set", slog.Int64("user_id", userID), slog.String("language", lang))
	return nil
}
