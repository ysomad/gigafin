package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

func TestPlanReportChildPlanOnly(t *testing.T) {
	t.Parallel()

	tree := []domain.Category{{ID: 1, Kind: domain.KindExpense, Name: "Food", Children: []domain.Category{
		{ID: 2, ParentID: 1, Kind: domain.KindExpense, Name: "Delivery"},
	}}}
	plans := []domain.Plan{{CategoryID: 2, Currency: "RUB", Amount: money.FromInt(5000)}}

	rows := domain.PlanReport(tree, nil, plans, domain.KindExpense)
	require.Len(t, rows, 2, "the cat1 row holds the cat2 row")
	assert.Equal(t, int64(1), rows[0].Category.ID)
	assert.False(t, rows[0].HasPlan)
	assert.Equal(t, int64(2), rows[1].Category.ID)
	assert.True(t, rows[1].HasPlan)
}

func TestPlanReportSortedBySpent(t *testing.T) {
	t.Parallel()

	tree := []domain.Category{
		{ID: 1, Kind: domain.KindExpense, Name: "Home"},
		{ID: 2, Kind: domain.KindExpense, Name: "Food", Children: []domain.Category{
			{ID: 3, ParentID: 2, Kind: domain.KindExpense, Name: "Cafe"},
			{ID: 4, ParentID: 2, Kind: domain.KindExpense, Name: "Groceries"},
		}},
	}
	sums := []domain.CategorySum{
		{CategoryID: 1, Currency: "RUB", Total: money.FromInt(1000)},
		{CategoryID: 3, Currency: "RUB", Total: money.FromInt(2000)},
		{CategoryID: 4, Currency: "RUB", Total: money.FromInt(8000)},
		{CategoryID: 1, Currency: "USD", Total: money.FromInt(50)},
	}

	rows := domain.PlanReport(tree, sums, nil, domain.KindExpense)
	got := make([]string, len(rows))
	for i, r := range rows {
		got[i] = r.Currency + " " + r.Category.Name
	}
	assert.Equal(t, []string{"RUB Food", "RUB Groceries", "RUB Cafe", "RUB Home", "USD Home"}, got)
}

func TestCarryPlans(t *testing.T) {
	t.Parallel()

	month := func(s string) timex.Month {
		m, err := timex.ParseMonth(s)
		require.NoError(t, err)
		return m
	}
	plan := func(cat int64, m, cur string, amount int64, repeating bool) domain.Plan {
		return domain.Plan{
			CategoryID: cat, Month: month(m), Currency: cur, Amount: money.FromInt(amount), Repeating: repeating,
		}
	}

	tests := []struct {
		name   string
		in     []domain.Plan
		to     string
		expect []domain.Plan
	}{
		{
			name:   "latest repeating wins",
			in:     []domain.Plan{plan(1, "2026-10", "RUB", 100, true), plan(1, "2026-11", "RUB", 200, true)},
			to:     "2026-12",
			expect: []domain.Plan{plan(1, "2026-12", "RUB", 200, true)},
		},
		{
			name:   "one month change is not carried",
			in:     []domain.Plan{plan(1, "2026-10", "RUB", 100, true), plan(1, "2026-11", "RUB", 200, false)},
			to:     "2026-12",
			expect: []domain.Plan{plan(1, "2026-12", "RUB", 100, true)},
		},
		{
			name: "zero stops carrying",
			in:   []domain.Plan{plan(1, "2026-10", "RUB", 100, true), plan(1, "2026-11", "RUB", 0, true)},
			to:   "2026-12",
		},
		{
			name: "currencies carry separately",
			in:   []domain.Plan{plan(1, "2026-10", "RUB", 100, true), plan(1, "2026-11", "USD", 5, true)},
			to:   "2026-12",
			expect: []domain.Plan{
				plan(1, "2026-12", "RUB", 100, true), plan(1, "2026-12", "USD", 5, true),
			},
		},
		{
			name: "plans of month and later are ignored",
			in:   []domain.Plan{plan(1, "2026-12", "RUB", 100, true), plan(2, "2027-01", "RUB", 5, true)},
			to:   "2026-12",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expect, domain.CarryPlans(tt.in, month(tt.to)))
		})
	}
}

func TestPlanRowLeftPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  domain.PlanRow
		want int64
	}{
		{"no plan", domain.PlanRow{Spent: money.FromInt(100)}, 0},
		{"zero plan", domain.PlanRow{HasPlan: true, Spent: money.FromInt(100)}, 0},
		{"under", domain.PlanRow{HasPlan: true, Plan: money.FromInt(1000), Spent: money.FromInt(300)}, 70},
		{"exact", domain.PlanRow{HasPlan: true, Plan: money.FromInt(1000), Spent: money.FromInt(1000)}, 0},
		{"over", domain.PlanRow{HasPlan: true, Plan: money.FromInt(1000), Spent: money.FromInt(1200)}, -20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.row.LeftPercent())
		})
	}
}

func TestPlanTotals(t *testing.T) {
	t.Parallel()

	rows := []domain.PlanRow{
		{Level: 1, Currency: "RUB", HasPlan: true, Plan: money.FromInt(1000), Spent: money.FromInt(300)},
		{Level: 2, Currency: "RUB", HasPlan: true, Plan: money.FromInt(400), Spent: money.FromInt(200)},
		{Level: 1, Currency: "RUB", Spent: money.FromInt(50)},
		{Level: 2, Currency: "RUB", HasPlan: true, Plan: money.FromInt(500), Spent: money.FromInt(600)},
		{Level: 1, Currency: "USD", HasPlan: true, Plan: money.FromInt(10), Spent: money.FromInt(5)},
	}
	got := domain.PlanTotals(rows)
	require.Len(t, got, 2)
	assert.Equal(t, "RUB", got[0].Currency)
	assert.True(t, money.FromInt(1500).Equal(got[0].Plan), "cat2 under a planned cat1 is not added")
	assert.True(t, money.FromInt(900).Equal(got[0].Spent))
	assert.Equal(t, "USD", got[1].Currency)
	assert.True(t, money.FromInt(10).Equal(got[1].Plan))
}
