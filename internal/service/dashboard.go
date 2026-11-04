package service

import (
	"context"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// _trendMonths is how many months a dashboard trend covers, up to its month.
const _trendMonths = 12

// _topPayees is how many payees per currency a dashboard shows.
const _topPayees = 10

// Dashboard reports an overview of a user's finances.
type Dashboard struct {
	store *repository.Store
}

// Get returns account totals, expenses of month by category compared with compareMonth, top
// payees of month, and over the year up to month: income vs expense, expenses by category and net worth.
func (s *Dashboard) Get(ctx context.Context, userID int64, month, compareMonth timex.Month) (domain.Dashboard, error) {
	r := s.store.Read()
	accounts, err := r.Accounts.List(ctx, userID)
	if err != nil {
		return domain.Dashboard{}, err
	}
	cats, err := r.Categories.List(ctx, userID)
	if err != nil {
		return domain.Dashboard{}, err
	}
	sums, err := r.Operations.SumByCategory(ctx, userID, month.First(), month.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	compareSums, err := r.Operations.SumByCategory(ctx, userID, compareMonth.First(), compareMonth.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	payees, err := r.Operations.SumByPayee(ctx, userID, month.First(), month.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	from := month
	for range _trendMonths - 1 {
		from = from.Prev()
	}
	monthly, err := r.Operations.SumByMonth(ctx, userID, from.First(), month.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	catMonthly, err := r.Operations.SumByCategoryMonth(ctx, userID, from.First(), month.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	changes, err := r.Operations.BalanceChanges(ctx, userID, month.Last())
	if err != nil {
		return domain.Dashboard{}, err
	}
	var months []timex.Month
	for m := from; !m.First().After(month.First()); m = m.Next() {
		months = append(months, m)
	}
	tree := domain.NewTree(cats)
	return domain.Dashboard{
		Month:        month,
		Totals:       domain.Totals(accounts),
		Trends:       domain.NewTrends(monthly, from, month),
		CompareMonth: compareMonth,
		Expenses: domain.CategoryShares(
			domain.PlanReport(tree, sums, nil, domain.KindExpense),
			domain.PlanReport(tree, compareSums, nil, domain.KindExpense),
		),
		TrendMonths:    months,
		CategoryTrends: domain.NewCategoryTrends(tree, catMonthly, domain.KindExpense, from, month),
		NetWorth:       domain.NewNetWorth(accounts, changes, from, month),
		Payees:         domain.TopPayees(payees, _topPayees),
	}, nil
}
