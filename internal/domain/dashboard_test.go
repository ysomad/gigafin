package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

func TestBuildTrends(t *testing.T) {
	t.Parallel()

	month := func(s string) timex.Month {
		m, err := timex.ParseMonth(s)
		require.NoError(t, err)
		return m
	}
	sums := []domain.MonthSum{
		{Month: month("2026-11"), Type: domain.OpIncome, Currency: "USD", Total: money.FromInt(10)},
		{Month: month("2026-09"), Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(300)},
		{Month: month("2026-11"), Type: domain.OpIncome, Currency: "RUB", Total: money.FromInt(1000)},
		{Month: month("2026-11"), Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(400)},
		{Month: month("2026-11"), Type: domain.OpTransfer, Currency: "RUB", Total: money.FromInt(5000)},
		{Month: month("2026-11"), Type: domain.OpAdjustment, Currency: "EUR", Total: money.FromInt(7)},
		{Month: month("2026-08"), Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(1)},
	}

	trends := domain.NewTrends(sums, month("2026-09"), month("2026-11"))
	require.Len(t, trends, 2, "transfers and adjustments are skipped")

	rub := trends[0]
	assert.Equal(t, "RUB", rub.Currency)
	require.Len(t, rub.Months, 3, "every month filled")
	assert.Equal(t, "2026-09", rub.Months[0].Month.String())
	assert.Equal(t, "300", rub.Months[0].Expense.String())
	assert.Equal(t, "2026-10", rub.Months[1].Month.String())
	assert.True(t, rub.Months[1].Income.IsZero())
	assert.Equal(t, "600", rub.Last().Net().String())

	assert.Equal(t, "USD", trends[1].Currency)
	assert.Equal(t, "10", trends[1].Last().Income.String())
}

func TestCategoryShares(t *testing.T) {
	t.Parallel()

	food := domain.Category{ID: 1, Name: "Food"}
	home := domain.Category{ID: 2, Name: "Home"}
	rows := []domain.PlanRow{
		{Category: food, Level: 1, Currency: "RUB", Spent: money.FromInt(250)},
		{Category: domain.Category{ID: 3, Name: "Cafe"}, Level: 2, Currency: "RUB", Spent: money.FromInt(250)},
		{Category: home, Level: 1, Currency: "RUB", Spent: money.FromInt(750)},
		{Category: home, Level: 1, Currency: "USD", Spent: money.FromInt(5)},
		{Category: domain.Category{ID: 4, Name: "Gifts"}, Level: 1, Currency: "RUB", HasPlan: true},
	}

	shares := domain.CategoryShares(rows, nil)
	require.Len(t, shares, 3, "only top level with spending")
	assert.Equal(t, "Home", shares[0].Category.Name)
	assert.Equal(t, int64(75), shares[0].Percent)
	assert.Equal(t, "Food", shares[1].Category.Name)
	assert.Equal(t, int64(25), shares[1].Percent)
	assert.Equal(t, "USD", shares[2].Currency)
	assert.Equal(t, int64(100), shares[2].Percent)
}

func TestCategorySharesCompare(t *testing.T) {
	t.Parallel()

	food := domain.Category{ID: 1, Name: "Food"}
	home := domain.Category{ID: 2, Name: "Home"}
	rows := []domain.PlanRow{
		{Category: food, Level: 1, Currency: "RUB", Spent: money.FromInt(150)},
	}
	prev := []domain.PlanRow{
		{Category: food, Level: 1, Currency: "RUB", Spent: money.FromInt(100)},
		{Category: home, Level: 1, Currency: "RUB", Spent: money.FromInt(300)},
		{Category: home, Level: 1, Currency: "USD", Spent: money.FromInt(5)},
	}

	shares := domain.CategoryShares(rows, prev)
	require.Len(t, shares, 3)
	assert.Equal(t, "Food", shares[0].Category.Name)
	assert.Equal(t, int64(50), shares[0].Change())
	assert.Equal(t, int64(100), shares[0].Percent)
	assert.Equal(t, "Home", shares[1].Category.Name, "spent in prev only")
	assert.True(t, shares[1].Spent.IsZero())
	assert.Equal(t, int64(-100), shares[1].Change())
	assert.Equal(t, "USD", shares[2].Currency)
	assert.Equal(t, int64(0), shares[2].Percent, "no spending in the currency")
}

func TestBuildCategoryTrends(t *testing.T) {
	t.Parallel()

	month := func(s string) timex.Month {
		m, err := timex.ParseMonth(s)
		require.NoError(t, err)
		return m
	}
	tree := domain.NewTree([]domain.Category{
		{ID: 1, Name: "Food", Kind: domain.KindExpense},
		{ID: 2, Name: "Cafe", Kind: domain.KindExpense, ParentID: 1},
		{ID: 3, Name: "Home", Kind: domain.KindExpense},
		{ID: 4, Name: "Work", Kind: domain.KindIncome},
	})
	sums := []domain.CategoryMonthSum{
		{CategoryID: 1, Month: month("2026-09"), Currency: "RUB", Total: money.FromInt(100)},
		{CategoryID: 2, Month: month("2026-09"), Currency: "RUB", Total: money.FromInt(50)},
		{CategoryID: 2, Month: month("2026-11"), Currency: "RUB", Total: money.FromInt(30)},
		{CategoryID: 3, Month: month("2026-10"), Currency: "RUB", Total: money.FromInt(500)},
		{CategoryID: 4, Month: month("2026-10"), Currency: "RUB", Total: money.FromInt(9000)},
		{CategoryID: 1, Month: month("2026-08"), Currency: "RUB", Total: money.FromInt(1)},
	}

	trends := domain.NewCategoryTrends(tree, sums, domain.KindExpense, month("2026-09"), month("2026-11"))
	require.Len(t, trends, 2, "other kinds and months are left out")
	assert.Equal(t, "Home", trends[0].Category.Name, "largest first")
	food := trends[1]
	assert.Equal(t, "Food", food.Category.Name)
	require.Len(t, food.Months, 3)
	assert.Equal(t, "150", food.Months[0].String(), "cat2 counts in its cat1")
	assert.True(t, food.Months[1].IsZero())
	assert.Equal(t, "30", food.Months[2].String())
	assert.Equal(t, "60", food.Average().String())
}

func TestBuildNetWorth(t *testing.T) {
	t.Parallel()

	month := func(s string) timex.Month {
		m, err := timex.ParseMonth(s)
		require.NoError(t, err)
		return m
	}
	accounts := []domain.Account{{ID: 1}, {ID: 2}, {ID: 3, Archived: true}, {ID: 4}}
	changes := []domain.BalanceChange{
		{Month: month("2026-01"), AccountID: 1, Currency: "RUB", Total: money.FromInt(1000)},
		{Month: month("2026-10"), AccountID: 2, Currency: "RUB", Total: money.FromInt(-300)},
		{Month: month("2026-11"), AccountID: 1, Currency: "RUB", Total: money.FromInt(50)},
		{Month: month("2026-10"), AccountID: 3, Currency: "RUB", Total: money.FromInt(7)},
		{Month: month("2026-01"), AccountID: 4, Currency: "USD", Total: money.FromInt(5)},
		{Month: month("2026-02"), AccountID: 4, Currency: "USD", Total: money.FromInt(-5)},
	}

	trends := domain.NewNetWorth(accounts, changes, month("2026-09"), month("2026-11"))
	require.Len(t, trends, 1, "archived accounts and currencies at zero are left out")
	rub := trends[0]
	require.Len(t, rub.Months, 3)
	assert.Equal(t, "1000", rub.Months[0].Balance.String(), "changes before from count")
	assert.Equal(t, "700", rub.Months[1].Balance.String(), "debts subtract")
	assert.Equal(t, "750", rub.Months[2].Balance.String())
}

func TestTopPayees(t *testing.T) {
	t.Parallel()

	sums := []domain.PayeeSum{
		{Payee: "A", Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(10)},
		{Payee: "B", Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(30)},
		{Payee: "C", Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(20)},
		{Payee: "Boss", Type: domain.OpIncome, Currency: "RUB", Total: money.FromInt(900)},
		{Payee: " ", Type: domain.OpExpense, Currency: "RUB", Total: money.FromInt(900)},
		{Payee: "D", Type: domain.OpExpense, Currency: "USD", Total: money.FromInt(1)},
	}

	top := domain.TopPayees(sums, 2)
	require.Len(t, top, 3)
	assert.Equal(t, "B", top[0].Payee)
	assert.Equal(t, "C", top[1].Payee)
	assert.Equal(t, "D", top[2].Payee)
}
