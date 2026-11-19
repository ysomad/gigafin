package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/service"
)

// newScheduler returns a scheduler with the background jobs registered; they run once
// it is started.
//
//nolint:ireturn // gocron exposes its scheduler only as an interface
func newScheduler(svc *service.Services, log *slog.Logger) (gocron.Scheduler, error) {
	sched, err := gocron.NewScheduler(gocron.WithLogger(log))
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}
	// every job runs hourly: carrying plans since a month starts at a different time
	// in each user's time zone, and the rate sync to pick up a new rate soon after it is
	// set, sending no requests when nothing is missing or every user has rates off
	jobs := []struct {
		name string
		run  func(ctx context.Context) (int64, error)
	}{
		{"delete_expired_sessions", svc.Auth.DeleteExpiredSessions},
		{"carry_plans", svc.Plans.CarryAll},
		{"sync_exchange_rates", func(ctx context.Context) (int64, error) {
			n, err := svc.Rates.Sync(ctx)
			return int64(n), err
		}},
	}
	for _, job := range jobs {
		// ctx is the job context, gocron cancels it on Shutdown
		_, err = sched.NewJob(
			gocron.DurationJob(time.Hour),
			gocron.NewTask(func(ctx context.Context) {
				log := log.With(slog.String("job", job.name))
				start := time.Now()
				n, err := job.run(ctx)
				took := slog.Int64("duration_ms", time.Since(start).Milliseconds())
				switch {
				case err != nil && ctx.Err() == nil:
					log.ErrorContext(ctx, "job failed", slog.Int64("count", n), took, slog.Any("error", err))
				case err != nil:
					log.DebugContext(ctx, "job cancelled", slog.Int64("count", n), took)
				case n > 0:
					log.InfoContext(ctx, "job done", slog.Int64("count", n), took)
				default:
					log.DebugContext(ctx, "job done", slog.Int64("count", n), took)
				}
			}),
			gocron.WithName(job.name),
			gocron.WithStartAt(gocron.WithStartImmediately()),
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return nil, fmt.Errorf("schedule %s: %w", job.name, errors.Join(err, sched.Shutdown()))
		}
	}
	return sched, nil
}
