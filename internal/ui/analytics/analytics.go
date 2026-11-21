package analytics

import (
	"maps"
	"net/http"
	"slices"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the analytics.
type Handler struct {
	app *kit.App
}

// New returns the handler of app.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of the analytics.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /{$}": h.analytics,
	}
}

func (h *Handler) analytics(w http.ResponseWriter, r *http.Request, user domain.User) error {
	month := timex.MonthOf(h.app.Svc.Today(user))
	if s := r.URL.Query().Get("month"); s != "" {
		var err error
		if month, err = timex.ParseMonth(s); err != nil {
			return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), s)
		}
	}
	compareMonth := month.Prev()
	if s := r.URL.Query().Get("compare"); s != "" {
		var err error
		if compareMonth, err = timex.ParseMonth(s); err != nil {
			return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Compare with"), s)
		}
	}
	an, err := h.app.Svc.Analytics.Get(r.Context(), user.ID, month, compareMonth)
	if err != nil {
		return err
	}
	months, err := h.app.Svc.Operations.Months(
		r.Context(),
		user.ID,
		timex.MonthOf(h.app.Svc.Today(user)),
		month,
		compareMonth,
	)
	if err != nil {
		return err
	}
	d := analyticsData{
		An:     an,
		From:   timex.FormatDate(month.First()),
		To:     timex.FormatDate(month.Last()),
		Months: months,
	}
	// months is a contiguous range, latest first, always holding month and compareMonth
	d.Prev, d.Next = timex.Neighbors(months, month)
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
	n := len(an.TrendMonths)
	chartMonths := make([]int64, n)
	for i, m := range an.TrendMonths {
		chartMonths[i] = m.First().Unix()
	}
	charts := make(map[string]*trendChart, len(an.Trends))
	for _, t := range an.Trends {
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
	for _, t := range an.NetWorth {
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

	return kit.Render(h.app, w, r, http.StatusOK, analyticsPage, kit.Page{
		User:      &user,
		Nav:       "analytics",
		Title:     "Analytics %s",
		TitleArgs: []any{kit.T(r.Context(), month.First().Format("January")) + " " + month.First().Format("2006")},
	}, d, kit.Failure{})
}
