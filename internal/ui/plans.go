package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

type plansData struct {
	Month timex.Month
	// Months are the months with operations, latest first, to pick Month from.
	Months     []timex.Month
	Prev       string
	Next       string
	From       string
	To         string
	Kind       domain.Kind
	Totals     []planItem
	Planned    []planItem
	Unplanned  []planItem
	Categories []domain.Category
	Currencies []string
	// Day of Days is how far the month has gone: Days for a past month, 0 for a future one
	Day  int
	Days int
	// Pace is Day as a share of Days in percent, the spending expected by now
	Pace int
}

// planItem is a plan row as shown: under its cat1 Parent for a cat2, with the
// unplanned cat2 it Includes, and a State of "over", "exceeded" or "".
type planItem struct {
	Row      domain.PlanRow
	Parent   string
	Includes []domain.PlanRow
	State    string
}

func (u *UI) plans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	today := u.svc.Today(user)
	month := timex.MonthOf(today)
	if s := q.Get("month"); s != "" {
		var err error
		if month, err = timex.ParseMonth(s); err != nil {
			return fmt.Errorf("month: %w", err)
		}
	}
	kind, err := domain.ParseKind(q.Get("kind"))
	if err != nil {
		kind = domain.KindExpense
	}
	rows, tree, err := u.svc.Plans.Report(r.Context(), user.ID, month, kind)
	if err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	months, err := u.svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(today), month)
	if err != nil {
		return err
	}
	d := plansData{
		Month:      month,
		Months:     months,
		Prev:       month.Prev().String(),
		Next:       month.Next().String(),
		From:       timex.FormatDate(month.First()),
		To:         timex.FormatDate(month.Last()),
		Kind:       kind,
		Categories: tree,
		Currencies: domain.AccountCurrencies(accounts),
		Days:       month.Last().Day(),
	}
	switch cur := timex.MonthOf(today).String(); {
	case month.String() == cur:
		d.Day = today.Day()
	case month.String() < cur:
		d.Day = d.Days
	}
	d.Pace = d.Day * 100 / d.Days
	state := func(r domain.PlanRow) string {
		switch {
		case r.Left().IsNegative() && kind == domain.KindIncome:
			return "exceeded"
		case r.Left().IsNegative():
			return "over"
		default:
			return ""
		}
	}
	totals := domain.PlanTotals(rows)
	d.Totals = make([]planItem, len(totals))
	for i, t := range totals {
		d.Totals[i] = planItem{Row: t, State: state(t)}
	}
	d.Planned, d.Unplanned = groupPlanRows(rows, state)
	return u.render(w, r, http.StatusOK, "plans", view{
		User: &user, Nav: "plans", Title: "Plans %s", TitleArgs: []any{month}, Data: d,
	})
}

// groupPlanRows splits report rows into plans, each cat1 with its unplanned cat2,
// and rows spent without a plan.
func groupPlanRows(
	rows []domain.PlanRow, state func(domain.PlanRow) string,
) (planned, unplanned []planItem) {
	// rows are groups of a cat1 followed by its cat2
	for i := 0; i < len(rows); {
		j := i + 1
		for j < len(rows) && rows[j].Level == 2 {
			j++
		}
		c1, children := rows[i], rows[i+1:j]
		i = j
		if c1.HasPlan {
			it := planItem{Row: c1, State: state(c1)}
			var plannedChildren []planItem
			for _, c2 := range children {
				if c2.HasPlan {
					plannedChildren = append(
						plannedChildren,
						planItem{Row: c2, Parent: c1.Category.Name, State: state(c2)},
					)
				} else {
					it.Includes = append(it.Includes, c2)
				}
			}
			planned = append(planned, it)
			planned = append(planned, plannedChildren...)
			continue
		}
		if !slices.ContainsFunc(children, func(c2 domain.PlanRow) bool { return c2.HasPlan }) {
			unplanned = append(unplanned, planItem{Row: c1, Includes: children})
			continue
		}
		// the cat1 total would repeat its planned cat2, so its cat2 show on their own
		for _, c2 := range children {
			if c2.HasPlan {
				planned = append(planned, planItem{Row: c2, Parent: c1.Category.Name, State: state(c2)})
			} else {
				unplanned = append(unplanned, planItem{Row: c2, Parent: c1.Category.Name})
			}
		}
	}
	return planned, unplanned
}

func (u *UI) setPlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Category  int64  `form:"category"`
		Month     string `form:"month"`
		Kind      string `form:"kind"`
		Currency  string `form:"currency"`
		Amount    string `form:"amount"`
		Repeating bool   `form:"repeating"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	// an empty amount removes only the month's plan; "0" with repeating stops carrying
	amount := money.Amount{}
	if f.Amount != "" {
		var err error
		if amount, err = money.Parse(f.Amount); err != nil {
			return domain.NewError(errors.ErrInvalid, "amount: enter a number")
		}
	} else {
		f.Repeating = false
	}
	month, err := timex.ParseMonth(f.Month)
	if err != nil {
		return fmt.Errorf("month: %w", err)
	}
	// category 0 is not found
	if err := u.svc.Plans.Set(
		r.Context(), user.ID, f.Category, month, f.Currency, amount, f.Repeating,
	); err != nil {
		return err
	}
	redirect(w, r, "/plans?"+url.Values{"month": {f.Month}, "kind": {f.Kind}}.Encode())
	return nil
}

func (u *UI) fillPlans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Month string `form:"month"`
		Kind  string `form:"kind"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	month, err := timex.ParseMonth(f.Month)
	if err != nil {
		return fmt.Errorf("month: %w", err)
	}
	if _, err := u.svc.Plans.Fill(r.Context(), user.ID, month); err != nil {
		return err
	}
	redirect(w, r, "/plans?"+url.Values{"month": {f.Month}, "kind": {f.Kind}}.Encode())
	return nil
}
