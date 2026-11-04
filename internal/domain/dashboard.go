package domain

import (
	"slices"
	"strings"

	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// Dashboard is an overview of a user's finances for a month.
type Dashboard struct {
	Month  timex.Month
	Totals []Total
	// Trends are income vs expense per currency over the year up to Month.
	Trends []Trend
	// CompareMonth is the month Expenses are compared with.
	CompareMonth timex.Month
	// Expenses are top level expense categories of Month, largest first.
	Expenses []CategoryShare
	// TrendMonths are the months of CategoryTrends and NetWorth, oldest first.
	TrendMonths []timex.Month
	// CategoryTrends are top level expense categories per month over the year up to Month.
	CategoryTrends []CategoryTrend
	// NetWorth is the month-end net worth per currency over the year up to Month.
	NetWorth []NetWorthTrend
	// Payees are the payees of Month that took the most, per currency.
	Payees []PayeeSum
}

// MonthSum is the total of operations of one type in a month and currency.
type MonthSum struct {
	Month    timex.Month
	Type     OpType
	Currency string
	Total    money.Amount
}

// MonthTotal is income and expense of a month in one currency.
type MonthTotal struct {
	Month   timex.Month
	Income  money.Amount
	Expense money.Amount
}

// Net returns income minus expense.
func (t MonthTotal) Net() money.Amount {
	return t.Income.Sub(t.Expense)
}

// Trend is income and expense of one currency for consecutive months, oldest first.
type Trend struct {
	Currency string
	Months   []MonthTotal
}

// Last returns the totals of the latest month.
func (t Trend) Last() MonthTotal {
	return t.Months[len(t.Months)-1]
}

// NewTrends returns a trend per currency with income or expense in sums, covering
// every month from..to and ordered by currency. Transfers and adjustments are not
// income or expense and are skipped.
func NewTrends(sums []MonthSum, from, to timex.Month) []Trend {
	var months []timex.Month
	for m := from; !m.First().After(to.First()); m = m.Next() {
		months = append(months, m)
	}
	index := make(map[string]int, len(months))
	for i, m := range months {
		index[m.String()] = i
	}
	byCur := make(map[string]Trend)
	for _, s := range sums {
		i, ok := index[s.Month.String()]
		if !ok || (s.Type != OpIncome && s.Type != OpExpense) {
			continue
		}
		t, ok := byCur[s.Currency]
		if !ok {
			t = Trend{Currency: s.Currency, Months: make([]MonthTotal, len(months))}
			for j, m := range months {
				t.Months[j].Month = m
			}
			byCur[s.Currency] = t
		}
		if s.Type == OpIncome {
			t.Months[i].Income = t.Months[i].Income.Add(s.Total)
		} else {
			t.Months[i].Expense = t.Months[i].Expense.Add(s.Total)
		}
	}
	trends := make([]Trend, 0, len(byCur))
	for _, t := range byCur {
		trends = append(trends, t)
	}
	slices.SortFunc(trends, func(a, b Trend) int { return strings.Compare(a.Currency, b.Currency) })
	return trends
}

// CategoryShare is what a top level category took of all spending in a currency.
type CategoryShare struct {
	Category Category
	Currency string
	Spent    money.Amount
	// Percent is of the currency total, 0..100.
	Percent int64
	// Prev is what the category took in the compared month.
	Prev money.Amount
}

// Change returns how much Spent differs from Prev in percent of Prev, 0 without Prev.
func (s CategoryShare) Change() int64 {
	if !s.Prev.IsPositive() {
		return 0
	}
	return s.Spent.Sub(s.Prev).PercentOf(s.Prev)
}

// CategoryShares returns top level rows of a plan report with spending, compared with
// the rows prev of another month, ordered by currency, then largest first. A category
// with spending in prev only is kept with nothing spent.
func CategoryShares(rows, prev []PlanRow) []CategoryShare {
	totals := make(map[string]money.Amount)
	index := make(map[catCur]int)
	var shares []CategoryShare
	for _, r := range rows {
		if r.Level != 1 || !r.Spent.IsPositive() {
			continue
		}
		totals[r.Currency] = totals[r.Currency].Add(r.Spent)
		index[catCur{id: r.Category.ID, cur: r.Currency}] = len(shares)
		shares = append(shares, CategoryShare{Category: r.Category, Currency: r.Currency, Spent: r.Spent})
	}
	for _, r := range prev {
		if r.Level != 1 || !r.Spent.IsPositive() {
			continue
		}
		if i, ok := index[catCur{id: r.Category.ID, cur: r.Currency}]; ok {
			shares[i].Prev = r.Spent
			continue
		}
		shares = append(shares, CategoryShare{Category: r.Category, Currency: r.Currency, Prev: r.Spent})
	}
	for i, s := range shares {
		if t := totals[s.Currency]; t.IsPositive() {
			shares[i].Percent = s.Spent.PercentOf(t)
		}
	}
	slices.SortStableFunc(shares, func(a, b CategoryShare) int {
		if c := strings.Compare(a.Currency, b.Currency); c != 0 {
			return c
		}
		if c := b.Spent.Cmp(a.Spent); c != 0 {
			return c
		}
		return b.Prev.Cmp(a.Prev)
	})
	return shares
}

// CategoryMonthSum is the total of operations in a category, month and currency.
type CategoryMonthSum struct {
	CategoryID int64
	Month      timex.Month
	Currency   string
	Total      money.Amount
}

// CategoryTrend is what a top level category took in one currency per month, oldest
// first.
type CategoryTrend struct {
	Category Category
	Currency string
	Months   []money.Amount
	Total    money.Amount
}

// Average returns the total per month.
func (t CategoryTrend) Average() money.Amount {
	return t.Total.Div(int64(len(t.Months)))
}

// NewCategoryTrends returns a trend per top level category of kind in tree and
// currency with spending in sums, a cat2 counted in its cat1, covering every month
// from..to; ordered by currency, then largest total first.
func NewCategoryTrends(tree []Category, sums []CategoryMonthSum, kind Kind, from, to timex.Month) []CategoryTrend {
	var months []timex.Month
	for m := from; !m.First().After(to.First()); m = m.Next() {
		months = append(months, m)
	}
	index := make(map[string]int, len(months))
	for i, m := range months {
		index[m.String()] = i
	}
	rootOf := make(map[int64]Category, len(tree))
	for _, c1 := range tree {
		if c1.Kind != kind {
			continue
		}
		rootOf[c1.ID] = c1
		for _, c2 := range c1.Children {
			rootOf[c2.ID] = c1
		}
	}
	byKey := make(map[catCur]*CategoryTrend)
	for _, s := range sums {
		i, ok := index[s.Month.String()]
		if !ok {
			continue
		}
		root, ok := rootOf[s.CategoryID]
		if !ok {
			continue
		}
		k := catCur{id: root.ID, cur: s.Currency}
		t, ok := byKey[k]
		if !ok {
			t = &CategoryTrend{Category: root, Currency: s.Currency, Months: make([]money.Amount, len(months))}
			byKey[k] = t
		}
		t.Months[i] = t.Months[i].Add(s.Total)
		t.Total = t.Total.Add(s.Total)
	}
	trends := make([]CategoryTrend, 0, len(byKey))
	for _, t := range byKey {
		if t.Total.IsPositive() {
			trends = append(trends, *t)
		}
	}
	slices.SortFunc(trends, func(a, b CategoryTrend) int {
		if c := strings.Compare(a.Currency, b.Currency); c != 0 {
			return c
		}
		if c := b.Total.Cmp(a.Total); c != 0 {
			return c
		}
		return strings.Compare(a.Category.Name, b.Category.Name)
	})
	return trends
}

// BalanceChange is how much the balance of an account changed in a month.
type BalanceChange struct {
	Month     timex.Month
	AccountID int64
	Currency  string
	Total     money.Amount
}

// MonthBalance is a balance at the end of a month.
type MonthBalance struct {
	Month   timex.Month
	Balance money.Amount
}

// NetWorthTrend is the net worth of one currency at the end of consecutive months,
// oldest first.
type NetWorthTrend struct {
	Currency string
	Months   []MonthBalance
}

// NewNetWorth returns the month-end sum of balances of active accounts per currency
// for every month from..to, from changes since the first operation; ordered by
// currency. Liabilities have negative balances, so the sum is net of debts as in
// Total.Net. Currencies that stay at zero are left out.
func NewNetWorth(accounts []Account, changes []BalanceChange, from, to timex.Month) []NetWorthTrend {
	active := make(map[int64]bool, len(accounts))
	for _, a := range accounts {
		active[a.ID] = !a.Archived
	}
	byCur := make(map[string]map[string]money.Amount)
	for _, c := range changes {
		if !active[c.AccountID] {
			continue
		}
		m, ok := byCur[c.Currency]
		if !ok {
			m = make(map[string]money.Amount)
			byCur[c.Currency] = m
		}
		m[c.Month.String()] = m[c.Month.String()].Add(c.Total)
	}
	trends := make([]NetWorthTrend, 0, len(byCur))
	for cur, byMonth := range byCur {
		var balance money.Amount
		for k, v := range byMonth {
			if k < from.String() {
				balance = balance.Add(v)
			}
		}
		t := NetWorthTrend{Currency: cur}
		nonZero := false
		for m := from; !m.First().After(to.First()); m = m.Next() {
			balance = balance.Add(byMonth[m.String()])
			t.Months = append(t.Months, MonthBalance{Month: m, Balance: balance})
			nonZero = nonZero || !balance.IsZero()
		}
		if nonZero {
			trends = append(trends, t)
		}
	}
	slices.SortFunc(trends, func(a, b NetWorthTrend) int { return strings.Compare(a.Currency, b.Currency) })
	return trends
}

// PayeeSum is the total of operations of one type with a payee in a currency.
type PayeeSum struct {
	Payee    string
	Type     OpType
	Currency string
	Total    money.Amount
	Count    int64
}

// TopPayees returns up to n payees of expenses per currency, ordered by currency, then
// largest total first.
func TopPayees(sums []PayeeSum, n int) []PayeeSum {
	var out []PayeeSum
	for _, s := range sums {
		if s.Type == OpExpense && strings.TrimSpace(s.Payee) != "" && s.Total.IsPositive() {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b PayeeSum) int {
		if c := strings.Compare(a.Currency, b.Currency); c != 0 {
			return c
		}
		if c := b.Total.Cmp(a.Total); c != 0 {
			return c
		}
		return strings.Compare(a.Payee, b.Payee)
	})
	top := out[:0]
	seen := make(map[string]int)
	for _, s := range out {
		if seen[s.Currency] < n {
			seen[s.Currency]++
			top = append(top, s)
		}
	}
	return top
}
