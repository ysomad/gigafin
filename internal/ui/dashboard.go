package ui

import (
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/pages"
	"github.com/ysomad/gigafin/internal/ui/view"
)

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
	d := view.Dashboard{
		Dash:   dash,
		From:   timex.FormatDate(month.First()),
		To:     timex.FormatDate(month.Last()),
		Months: months,
	}
	// months is a contiguous range, latest first, always holding month and compareMonth
	newest, oldest := months[0].String(), months[len(months)-1].String()
	if month.String() != oldest {
		d.Prev = month.Prev().String()
	}
	if month.String() != newest {
		d.Next = month.Next().String()
	}
	// compare options leave out month, so its buttons step over it
	ci := slices.IndexFunc(months, func(m timex.Month) bool { return m.String() == compareMonth.String() })
	for _, m := range months[ci+1:] {
		if m.String() != month.String() {
			d.ComparePrev = m.String()
			break
		}
	}
	for _, m := range slices.Backward(months[:ci]) {
		if m.String() != month.String() {
			d.CompareNext = m.String()
			break
		}
	}
	// trends and net worth share TrendMonths but not always currencies
	n := len(dash.TrendMonths)
	chartMonths := make([]int64, n)
	for i, m := range dash.TrendMonths {
		chartMonths[i] = m.First().Unix()
	}
	charts := make(map[string]*view.TrendChart, len(dash.Trends))
	for _, t := range dash.Trends {
		c := &view.TrendChart{
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
			c = &view.TrendChart{
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
	d.Charts = make([]view.TrendChart, len(curs))
	for i, cur := range curs {
		d.Charts[i] = *charts[cur]
	}
	return u.render(w, r, http.StatusOK, pages.Dashboard, view.Page{
		User: &user, Nav: "dashboard", Title: "Dashboard %s", TitleArgs: []any{month}, Data: d,
	})
}
