package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Plans manages monthly plans and reports plan vs actual.
type Plans struct {
	store *repository.Store
	clock timex.Clock
	log   *slog.Logger
}

// Report returns plan vs actual for every category of kind that has a plan or
// operations in month, ordered as the category tree, and the tree itself.
func (s *Plans) Report(
	ctx context.Context, userID int64, month timex.Month, kind domain.Kind,
) ([]domain.PlanRow, []domain.Category, error) {
	r := s.store.Read()
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	plans, err := r.Plans.List(ctx, userID, month)
	if err != nil {
		return nil, nil, err
	}
	sums, err := r.Operations.SumByCategory(ctx, userID, month.First(), month.Last())
	if err != nil {
		return nil, nil, err
	}
	tree := domain.NewTree(cats)
	return domain.PlanReport(tree, sums, plans, kind), tree, nil
}

// Set sets the plan of a category for a month and currency. The latest repeating
// plan before a month is carried into it once, so a change to a month that is already
// carried past does not reach the months after it. A zero repeating plan stops
// carrying, and a zero one that is not repeating removes the month's plan.
func (s *Plans) Set(
	ctx context.Context, userID, categoryID int64, month timex.Month, currency string, amount money.Amount,
	repeating bool,
) error {
	b, err := domain.NewPlan(categoryID, month, currency, amount, repeating)
	if err != nil {
		return err
	}
	r := s.store.Write()
	if _, err := r.Categories.Get(ctx, userID, categoryID); err != nil {
		return err
	}
	if b.Amount.IsZero() && !b.Repeating {
		if err := r.Plans.Delete(ctx, userID, b); err != nil {
			return err
		}
		s.log.DebugContext(ctx, "plan removed", slog.Int64("user_id", userID), slog.Int64("category_id", categoryID),
			slog.String("month", month.String()))
		return nil
	}
	if err := r.Plans.Set(ctx, userID, b); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "plan set", slog.Int64("user_id", userID), slog.Int64("category_id", categoryID),
		slog.String("month", month.String()), slog.Bool("repeating", repeating))
	return nil
}

// Fill carries repeating plans of earlier months into month, keeping plans month
// already has. It returns the number of plans added.
func (s *Plans) Fill(ctx context.Context, userID int64, month timex.Month) (int64, error) {
	var n int64
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		var err error
		n, err = carryPlans(ctx, r, userID, month)
		return err
	})
	if err != nil {
		return n, err
	}
	s.log.DebugContext(ctx, "plans filled", slog.Int64("user_id", userID), slog.String("month", month.String()),
		slog.Int64("count", n))
	return n, nil
}

// CarryAll fills the current month of every user, in the user's time zone, once per
// month: a plan removed after that stays removed. A failed user does not stop the
// others. It returns the number of plans added.
func (s *Plans) CarryAll(ctx context.Context) (int64, error) {
	users, err := s.store.Read().Users.List(ctx)
	if err != nil {
		return 0, err
	}
	var (
		total int64
		errs  []error
	)
	for _, u := range users {
		month := timex.MonthOf(timex.Today(s.clock, u.Location()))
		err := s.store.InTx(ctx, func(r repository.Repos) error {
			first, err := r.Plans.MarkCarried(ctx, u.ID, month, s.clock.Now())
			if err != nil || !first {
				return err
			}
			n, err := carryPlans(ctx, r, u.ID, month)
			if err != nil {
				return err
			}
			total += n
			s.log.DebugContext(ctx, "plans carried", slog.Int64("user_id", u.ID), slog.String("month", month.String()),
				slog.Int64("count", n))
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("user %d: %w", u.ID, err))
		}
	}
	return total, errors.Join(errs...)
}

// carryPlans inserts the repeating plans of months before month into it, keeping
// plans month already has, and returns how many were inserted.
func carryPlans(ctx context.Context, r repository.Repos, userID int64, month timex.Month) (int64, error) {
	repeating, err := r.Plans.ListRepeating(ctx, userID, month)
	if err != nil {
		return 0, err
	}
	var n int64
	for _, b := range domain.CarryPlans(repeating, month) {
		ok, err := r.Plans.Insert(ctx, userID, b)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// List returns plans set for month, without zero ones that only stop carrying.
func (s *Plans) List(ctx context.Context, userID int64, month timex.Month) ([]domain.Plan, error) {
	plans, err := s.store.Read().Plans.List(ctx, userID, month)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(plans, func(b domain.Plan) bool { return b.Amount.IsZero() }), nil
}
