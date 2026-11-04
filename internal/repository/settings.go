package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

// Settings stores user settings.
type Settings struct {
	q *sqlc.Queries
}

// Get returns the user's settings, the defaults if the user never changed them.
func (r *Settings) Get(ctx context.Context, userID int64) (domain.Settings, error) {
	s, err := r.q.GetUserSettings(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Settings{}, nil
	}
	if err != nil {
		return domain.Settings{}, fmt.Errorf("get settings: %w", err)
	}
	return domain.Settings{
		ShowMinorUnits:  s.ShowMinorUnits,
		RateSource:      domain.RateSource(s.RateSource),
		PrimaryCurrency: s.PrimaryCurrency,
		Language:        s.Language,
	}, nil
}

// Set stores the user's settings.
func (r *Settings) Set(ctx context.Context, userID int64, s domain.Settings, now time.Time) error {
	err := r.q.UpsertUserSettings(ctx, sqlc.UpsertUserSettingsParams{
		UserID:          userID,
		ShowMinorUnits:  s.ShowMinorUnits,
		PrimaryCurrency: s.PrimaryCurrency,
		RateSource:      string(s.RateSource),
		Language:        s.Language,
		CreatedAt:       now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("set settings: %w", err)
	}
	return nil
}
