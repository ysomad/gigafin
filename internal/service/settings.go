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

// Set validates and stores all of the user's settings, replacing the ones stored.
func (s *Settings) Set(ctx context.Context, userID int64, settings domain.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if err := s.store.Write().Settings.Set(ctx, userID, settings, s.clock.Now()); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "settings set", slog.Int64("user_id", userID),
		slog.Bool("show_minor_units", settings.ShowMinorUnits),
		slog.String("rate_source", string(settings.RateSource)),
		slog.String("primary_currency", settings.PrimaryCurrency),
		slog.String("language", settings.Language),
		slog.Bool("auto_category_rules", settings.AutoCategoryRules))
	return nil
}
