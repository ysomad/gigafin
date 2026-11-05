package ui

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/timex"
)

type dashboardData struct {
	Dash domain.Dashboard
	Prev string
	Next string
	From string
	To   string
	// Charts are income vs expense with net worth per currency, ordered by currency.
	Charts []trendChart
	// Months are the months with operations, latest first, to pick Month and CompareMonth from.
	Months []timex.Month
}

// trendChart is a trend and net worth of a currency as chart series, read by dashboard.js.
type trendChart struct {
	Currency string    `json:"-"`
	Months   []int64   `json:"months"` // unix seconds of the first day
	Income   []float64 `json:"income"`
	Expense  []float64 `json:"expense"`
	// Balance is the month-end net worth, empty for a currency without one.
	Balance []float64 `json:"balance,omitempty"`
}

func (u *UI) dashboard(w http.ResponseWriter, r *http.Request, user domain.User) error {
	month := timex.MonthOf(u.svc.Today(user))
	if s := r.URL.Query().Get("month"); s != "" {
		var err error
		if month, err = timex.ParseMonth(s); err != nil {
			return fmt.Errorf("month: %w", err)
		}
	}
	compareMonth := month.Prev()
	if s := r.URL.Query().Get("compare"); s != "" {
		var err error
		if compareMonth, err = timex.ParseMonth(s); err != nil {
			return fmt.Errorf("compare: %w", err)
		}
	}
	dash, err := u.svc.Dashboard.Get(r.Context(), user.ID, month, compareMonth)
	if err != nil {
		return err
	}
	months, err := u.svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(u.svc.Today(user)), month, compareMonth)
	if err != nil {
		return err
	}
	d := dashboardData{
		Dash:   dash,
		Prev:   month.Prev().String(),
		Next:   month.Next().String(),
		From:   timex.FormatDate(month.First()),
		To:     timex.FormatDate(month.Last()),
		Months: months,
	}
	// trends and net worth share TrendMonths but not always currencies
	n := len(dash.TrendMonths)
	chartMonths := make([]int64, n)
	for i, m := range dash.TrendMonths {
		chartMonths[i] = m.First().Unix()
	}
	charts := make(map[string]*trendChart, len(dash.Trends))
	for _, t := range dash.Trends {
		c := &trendChart{
			Currency: t.Currency,
			Months:   chartMonths,
			Income:   make([]float64, n),
			Expense:  make([]float64, n),
		}
		for i, m := range t.Months {
			c.Income[i] = m.Income.Float64()
			c.Expense[i] = m.Expense.Float64()
		}
		charts[t.Currency] = c
	}
	for _, t := range dash.NetWorth {
		c, ok := charts[t.Currency]
		if !ok {
			c = &trendChart{
				Currency: t.Currency,
				Months:   chartMonths,
				Income:   make([]float64, n),
				Expense:  make([]float64, n),
			}
			charts[t.Currency] = c
		}
		c.Balance = make([]float64, n)
		for i, m := range t.Months {
			c.Balance[i] = m.Balance.Float64()
		}
	}
	curs := slices.Sorted(maps.Keys(charts))
	d.Charts = make([]trendChart, len(curs))
	for i, cur := range curs {
		d.Charts[i] = *charts[cur]
	}
	return u.render(w, r, http.StatusOK, "dashboard", view{
		User: &user, Nav: "dashboard", Title: "Dashboard %s", TitleArgs: []any{month}, Data: d,
	})
}
