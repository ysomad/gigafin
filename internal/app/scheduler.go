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
	// ctx is the job context, gocron cancels it on Shutdown
	_, err = sched.NewJob(
		gocron.DurationJob(time.Hour),
		gocron.NewTask(func(ctx context.Context) {
			log := log.With(slog.String("job", "delete_expired_sessions"))
			start := time.Now()
			n, err := svc.Auth.DeleteExpiredSessions(ctx)
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
		gocron.WithName("delete_expired_sessions"),
		gocron.WithStartAt(gocron.WithStartImmediately()),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return nil, fmt.Errorf("schedule delete expired sessions: %w", errors.Join(err, sched.Shutdown()))
	}
	// hourly, since a month starts at a different time in each user's time zone
	_, err = sched.NewJob(
		gocron.DurationJob(time.Hour),
		gocron.NewTask(func(ctx context.Context) {
			log := log.With(slog.String("job", "carry_plans"))
			start := time.Now()
			n, err := svc.Plans.CarryAll(ctx)
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
		gocron.WithName("carry_plans"),
		gocron.WithStartAt(gocron.WithStartImmediately()),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return nil, fmt.Errorf("schedule carry plans: %w", errors.Join(err, sched.Shutdown()))
	}
	// hourly to pick up a new rate soon after it is set; a sync with nothing missing
	// sends no requests, and none at all while every user has rates off
	_, err = sched.NewJob(
		gocron.DurationJob(time.Hour),
		gocron.NewTask(func(ctx context.Context) {
			log := log.With(slog.String("job", "sync_exchange_rates"))
			start := time.Now()
			n, err := svc.Rates.Sync(ctx)
			took := slog.Int64("duration_ms", time.Since(start).Milliseconds())
			switch {
			case err != nil && ctx.Err() == nil:
				log.ErrorContext(ctx, "job failed", slog.Int("count", n), took, slog.Any("error", err))
			case err != nil:
				log.DebugContext(ctx, "job cancelled", slog.Int("count", n), took)
			case n > 0:
				log.InfoContext(ctx, "job done", slog.Int("count", n), took)
			default:
				log.DebugContext(ctx, "job done", slog.Int("count", n), took)
			}
		}),
		gocron.WithName("sync_exchange_rates"),
		gocron.WithStartAt(gocron.WithStartImmediately()),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return nil, fmt.Errorf("schedule sync exchange rates: %w", errors.Join(err, sched.Shutdown()))
	}
	return sched, nil
}
