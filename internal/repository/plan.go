package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// Plans stores monthly plans per category and currency.
type Plans struct {
	q *sqlc.Queries
}

// List returns plans set for month.
func (r *Plans) List(ctx context.Context, userID int64, month timex.Month) ([]domain.Plan, error) {
	rows, err := r.q.ListPlans(ctx, sqlc.ListPlansParams{UserID: userID, Month: month.String()})
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	out := make([]domain.Plan, len(rows))
	for i, b := range rows {
		out[i] = domain.Plan{
			CategoryID: b.CategoryID,
			Month:      month,
			Currency:   b.Currency,
			Amount:     money.FromMinor(b.Amount, b.Currency),
			Repeating:  b.IsRepeating,
		}
	}
	return out, nil
}

// ListRepeating returns repeating plans of months before before, oldest first.
func (r *Plans) ListRepeating(ctx context.Context, userID int64, before timex.Month) ([]domain.Plan, error) {
	rows, err := r.q.ListRepeatingPlans(ctx, sqlc.ListRepeatingPlansParams{
		UserID: userID, Month: before.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list repeating plans: %w", err)
	}
	out := make([]domain.Plan, len(rows))
	for i, b := range rows {
		month, err := timex.ParseMonth(b.Month)
		if err != nil {
			return nil, fmt.Errorf("plan %d month: %w", b.ID, err)
		}
		out[i] = domain.Plan{
			CategoryID: b.CategoryID,
			Month:      month,
			Currency:   b.Currency,
			Amount:     money.FromMinor(b.Amount, b.Currency),
			Repeating:  b.IsRepeating,
		}
	}
	return out, nil
}

// Set stores b, replacing the plan of its category, month and currency.
func (r *Plans) Set(ctx context.Context, userID int64, b domain.Plan) error {
	amount, err := b.Amount.ToMinor(b.Currency)
	if err != nil {
		return fmt.Errorf("plan amount: %w", err)
	}
	err = r.q.UpsertPlan(ctx, sqlc.UpsertPlanParams{
		UserID: userID, CategoryID: b.CategoryID, Month: b.Month.String(), Currency: b.Currency, Amount: amount,
		IsRepeating: b.Repeating,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("upsert plan: %w", k)
	}
	if err != nil {
		return fmt.Errorf("upsert plan: %w", err)
	}
	return nil
}

// Insert stores b unless its category, month and currency already have a plan, and
// reports whether it was stored.
func (r *Plans) Insert(ctx context.Context, userID int64, b domain.Plan) (bool, error) {
	amount, err := b.Amount.ToMinor(b.Currency)
	if err != nil {
		return false, fmt.Errorf("plan amount: %w", err)
	}
	n, err := r.q.InsertPlanIfAbsent(ctx, sqlc.InsertPlanIfAbsentParams{
		UserID: userID, CategoryID: b.CategoryID, Month: b.Month.String(), Currency: b.Currency, Amount: amount,
		IsRepeating: b.Repeating,
	})
	if k, ok := constraint(err); ok {
		return false, fmt.Errorf("insert plan: %w", k)
	}
	if err != nil {
		return false, fmt.Errorf("insert plan: %w", err)
	}
	return n > 0, nil
}

// Delete removes the plan of b's category, month and currency, if any.
func (r *Plans) Delete(ctx context.Context, userID int64, b domain.Plan) error {
	err := r.q.DeletePlan(ctx, sqlc.DeletePlanParams{
		UserID: userID, CategoryID: b.CategoryID, Month: b.Month.String(), Currency: b.Currency,
	})
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	return nil
}

// MarkCarried records that repeating plans were carried into month, and reports
// whether they were not yet.
func (r *Plans) MarkCarried(ctx context.Context, userID int64, month timex.Month, now time.Time) (bool, error) {
	n, err := r.q.InsertPlanCarry(ctx, sqlc.InsertPlanCarryParams{
		UserID: userID, Month: month.String(), CreatedAt: now.Unix(),
	})
	if err != nil {
		return false, fmt.Errorf("mark plans carried: %w", err)
	}
	return n > 0, nil
}

// DeleteAll deletes every plan of the user and the record of carried months.
func (r *Plans) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserPlans(ctx, userID); err != nil {
		return fmt.Errorf("delete plans: %w", err)
	}
	if err := r.q.DeleteUserPlanCarries(ctx, userID); err != nil {
		return fmt.Errorf("delete plan carries: %w", err)
	}
	return nil
}
