package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// Plan is the planned amount of a category in a month and currency. A repeating
// plan is carried into following months, see CarryPlans.
type Plan struct {
	CategoryID int64
	Month      timex.Month
	Currency   string
	Amount     money.Amount
	Repeating  bool
}

// NewPlan validates a plan. A zero amount means no plan; a repeating one also
// stops carrying an earlier repeating plan.
func NewPlan(
	categoryID int64, month timex.Month, currency string, amount money.Amount, repeating bool,
) (Plan, error) {
	if month.IsZero() {
		return Plan{}, NewFieldError(errors.ErrInvalid, "month", "required")
	}
	cur := strings.ToUpper(strings.TrimSpace(currency))
	if !money.ValidCurrency(cur) {
		return Plan{}, NewFieldError(errors.ErrInvalid, "currency", "unsupported currency %q", cur)
	}
	if _, err := amount.ToMinor(cur); err != nil {
		return Plan{}, fmt.Errorf("amount: %w", err)
	}
	if amount.IsNegative() {
		return Plan{}, NewFieldError(errors.ErrInvalid, "amount", "must not be negative")
	}
	return Plan{CategoryID: categoryID, Month: month, Currency: cur, Amount: amount, Repeating: repeating}, nil
}

// CarryPlans returns the plans to carry into month to: for every category and
// currency, the latest repeating plan before to, unless its amount is zero, which
// stops carrying. A plan that is not repeating changes only its own month, so it is
// not carried.
func CarryPlans(repeating []Plan, to timex.Month) []Plan {
	latest := make(map[catCur]Plan, len(repeating))
	var keys []catCur
	for _, b := range repeating {
		if !b.Repeating || !b.Month.First().Before(to.First()) {
			continue
		}
		k := catCur{id: b.CategoryID, cur: b.Currency}
		prev, ok := latest[k]
		if !ok {
			keys = append(keys, k)
		}
		if !ok || prev.Month.First().Before(b.Month.First()) {
			latest[k] = b
		}
	}
	var out []Plan
	for _, k := range keys {
		b := latest[k]
		if b.Amount.IsZero() {
			continue
		}
		b.Month = to
		out = append(out, b)
	}
	return out
}

// CategorySum is the total of operations in a category in a currency.
type CategorySum struct {
	CategoryID int64
	Currency   string
	Total      money.Amount
}

// PlanRow is plan vs actual for one category and currency in a month. Spent of a
// cat1 includes all its cat2.
type PlanRow struct {
	Category  Category
	Level     int
	Currency  string
	Plan      money.Amount
	HasPlan   bool
	Repeating bool
	Spent     money.Amount
}

// Left returns plan minus spent.
func (r PlanRow) Left() money.Amount {
	return r.Plan.Sub(r.Spent)
}

// Percent returns spent as a share of plan, 0..100+, or 0 without a plan.
func (r PlanRow) Percent() int64 {
	if !r.HasPlan || r.Plan.IsZero() {
		return 0
	}
	return r.Spent.PercentOf(r.Plan)
}

// LeftPercent returns left as a share of plan, below 0 when overspent, or 0 without
// a plan.
func (r PlanRow) LeftPercent() int64 {
	if !r.HasPlan || r.Plan.IsZero() {
		return 0
	}
	return 100 - r.Percent()
}

// PlanTotals returns the planned rows summed per currency, as rows with only
// Currency, Plan and Spent set. A cat2 plan under a planned cat1 is part of the
// cat1 plan, so it is not added again.
func PlanTotals(rows []PlanRow) []PlanRow {
	var out []PlanRow
	parentPlanned := false
	for _, r := range rows {
		if r.Level == 1 {
			parentPlanned = r.HasPlan
		}
		if !r.HasPlan || (r.Level == 2 && parentPlanned) {
			continue
		}
		i := slices.IndexFunc(out, func(t PlanRow) bool { return t.Currency == r.Currency })
		if i < 0 {
			out = append(out, PlanRow{Currency: r.Currency, HasPlan: true})
			i = len(out) - 1
		}
		out[i].Plan = out[i].Plan.Add(r.Plan)
		out[i].Spent = out[i].Spent.Add(r.Spent)
	}
	return out
}

type catCur struct {
	id  int64
	cur string
}

// report holds spent and plan amounts per category and currency.
type report struct {
	spent map[catCur]money.Amount
	plan  map[catCur]Plan
}

// PlanReport returns plan vs actual for every category of kind in tree that has a
// plan or spending. Rows are grouped by currency, as amounts in different currencies
// do not compare, and ordered by spent, highest first, with cat2 under their cat1.
func PlanReport(tree []Category, sums []CategorySum, plans []Plan, kind Kind) []PlanRow {
	r := newReport(tree, sums, plans)
	// each group is a cat1 row followed by its cat2 rows
	var groups [][]PlanRow
	for _, c1 := range tree {
		if c1.Kind != kind {
			continue
		}
		for _, cur := range r.currencies(c1) {
			g := []PlanRow{r.row(c1, 1 /* level */, cur)}
			for _, c2 := range c1.Children {
				if r.has(c2.ID, cur) {
					g = append(g, r.row(c2, 2 /* level */, cur))
				}
			}
			slices.SortStableFunc(g[1:], func(a, b PlanRow) int { return b.Spent.Cmp(a.Spent) })
			groups = append(groups, g)
		}
	}
	slices.SortStableFunc(groups, func(a, b []PlanRow) int {
		if c := strings.Compare(a[0].Currency, b[0].Currency); c != 0 {
			return c
		}
		return b[0].Spent.Cmp(a[0].Spent)
	})
	var rows []PlanRow
	for _, g := range groups {
		rows = append(rows, g...)
	}
	return rows
}

func newReport(tree []Category, sums []CategorySum, plans []Plan) report {
	parentOf := make(map[int64]int64, len(tree))
	for _, c1 := range tree {
		for _, c2 := range c1.Children {
			parentOf[c2.ID] = c1.ID
		}
	}
	r := report{
		spent: make(map[catCur]money.Amount, len(sums)),
		plan:  make(map[catCur]Plan, len(plans)),
	}
	for _, s := range sums {
		k := catCur{id: s.CategoryID, cur: s.Currency}
		r.spent[k] = r.spent[k].Add(s.Total)
		if p, ok := parentOf[s.CategoryID]; ok {
			pk := catCur{id: p, cur: s.Currency}
			r.spent[pk] = r.spent[pk].Add(s.Total)
		}
	}
	for _, b := range plans {
		// a zero plan only stops a repeating one, there is nothing to report
		if !b.Amount.IsZero() {
			r.plan[catCur{id: b.CategoryID, cur: b.Currency}] = b
		}
	}
	return r
}

// currencies returns currencies with spending or a plan for the cat1 or any of its
// cat2, so a plan of a cat2 shows without one of its cat1.
func (r report) currencies(c1 Category) []string {
	ids := make([]int64, 1+len(c1.Children))
	ids[0] = c1.ID
	for i, c2 := range c1.Children {
		ids[i+1] = c2.ID
	}
	var out []string
	for k := range r.spent {
		if slices.Contains(ids, k.id) && !slices.Contains(out, k.cur) {
			out = append(out, k.cur)
		}
	}
	for k := range r.plan {
		if slices.Contains(ids, k.id) && !slices.Contains(out, k.cur) {
			out = append(out, k.cur)
		}
	}
	slices.Sort(out)
	return out
}

func (r report) has(id int64, cur string) bool {
	_, spent := r.spent[catCur{id: id, cur: cur}]
	_, plan := r.plan[catCur{id: id, cur: cur}]
	return spent || plan
}

func (r report) row(c Category, level int, cur string) PlanRow {
	b, has := r.plan[catCur{id: c.ID, cur: cur}]
	return PlanRow{
		Category:  c,
		Level:     level,
		Currency:  cur,
		Plan:      b.Amount,
		HasPlan:   has,
		Repeating: b.Repeating,
		Spent:     r.spent[catCur{id: c.ID, cur: cur}],
	}
}
