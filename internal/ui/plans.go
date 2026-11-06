package ui

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

type plansData struct {
	Month timex.Month
	// Months are the months with operations, latest first, to pick Month from.
	Months []timex.Month
	// Prev and Next are the adjacent months within Months, empty at its ends.
	Prev string
	Next string
	From string
	To   string
	// Sections are the plans of expenses, then of income.
	Sections []planSection
	// Day of Days is how far the month has gone: Days for a past month, 0 for a future one
	Day  int
	Days int
	// Pace is Day as a share of Days in percent, the spending expected by now
	Pace int
	// Posted are the values of a plan form post that failed, to fill its inputs again.
	Posted planForm
	// Open is the "category:currency" of the plan row posted last, kept open.
	Open string
}

// planForm is a posted plan form: the new plan form, with New set, or the form of a
// plan row, whose amount message is keyed by planKey.
type planForm struct {
	New       bool   `form:"new"`
	Category  int64  `form:"category"`
	Month     string `form:"month"`
	Currency  string `form:"currency"`
	Amount    string `form:"amount"`
	Repeating bool   `form:"repeating"`
}

// planSection is the plans of a Kind of categories, with their Totals per currency.
type planSection struct {
	Kind    domain.Kind
	Totals  []domain.PlanRow
	Planned []planItem
}

// planItem is a plan row as shown, under its cat1 Parent for a cat2.
type planItem struct {
	Row    domain.PlanRow
	Parent string
}

// planNewData is the new plan page: the form values of Posted, with Month parsed from
// it.
type planNewData struct {
	Month      timex.Month
	Months     []timex.Month
	Categories []domain.Category
	Currencies []string
	Posted     planForm
}

func (u *UI) plans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	return u.renderPlans(w, r, user, http.StatusOK, planForm{Month: q.Get("month")}, nil)
}

// renderPlans renders the plans of the month of f, and the values of f, if it is a form
// post that failed with viewErr.
func (u *UI) renderPlans(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f planForm, viewErr error,
) error {
	today := u.svc.Today(user)
	month := timex.MonthOf(today)
	if f.Month != "" {
		var err error
		if month, err = timex.ParseMonth(f.Month); err != nil {
			return fmt.Errorf("month: %w", err)
		}
	}
	// next month is open to plan ahead
	months, err := u.svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(today).Next(), month)
	if err != nil {
		return err
	}
	d := plansData{
		Month:  month,
		Months: months,
		From:   timex.FormatDate(month.First()),
		To:     timex.FormatDate(month.Last()),
		Days:   month.Last().Day(),
		Posted: f,
	}
	if f.Category != 0 && !f.New {
		d.Open = strconv.FormatInt(f.Category, 10) + ":" + f.Currency
	}
	// months is a contiguous range, latest first, always holding month
	if month.String() != months[len(months)-1].String() {
		d.Prev = month.Prev().String()
	}
	if month.String() != months[0].String() {
		d.Next = month.Next().String()
	}
	switch cur := timex.MonthOf(today).String(); {
	case month.String() == cur:
		d.Day = today.Day()
	case month.String() < cur:
		d.Day = d.Days
	}
	d.Pace = d.Day * 100 / d.Days
	for _, kind := range []domain.Kind{domain.KindExpense, domain.KindIncome} {
		rows, _, err := u.svc.Plans.Report(r.Context(), user.ID, month, kind)
		if err != nil {
			return err
		}
		sec := planSection{Kind: kind, Totals: domain.PlanTotals(rows)}
		// rows are groups of a cat1 followed by its cat2
		parent := ""
		for _, row := range rows {
			if row.Level == 1 {
				parent = row.Category.Name
			}
			if !row.HasPlan {
				continue
			}
			it := planItem{Row: row}
			if row.Level == 2 {
				it.Parent = parent
			}
			sec.Planned = append(sec.Planned, it)
		}
		d.Sections = append(d.Sections, sec)
	}
	return u.render(w, r, status, "plans", view{
		User: &user, Nav: "plans", Title: "Plans %s", TitleArgs: []any{month}, Error: viewErr, Form: "plan", Data: d,
	})
}

func (u *UI) newPlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	return u.renderNewPlan(w, r, user, http.StatusOK, planForm{
		New: true, Month: q.Get("month"), Repeating: true,
	}, nil)
}

// renderNewPlan renders the new plan form with the values of f, and viewErr of its
// failed post.
func (u *UI) renderNewPlan(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f planForm, viewErr error,
) error {
	today := u.svc.Today(user)
	d := planNewData{Month: timex.MonthOf(today), Posted: f}
	if f.Month != "" {
		var err error
		if d.Month, err = timex.ParseMonth(f.Month); err != nil {
			return fmt.Errorf("month: %w", err)
		}
	}
	var err error
	if d.Categories, err = u.svc.Categories.Tree(r.Context(), user.ID); err != nil {
		return err
	}
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	d.Currencies = domain.AccountCurrencies(accounts)
	if d.Months, err = u.svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(today).Next(), d.Month); err != nil {
		return err
	}
	return u.render(w, r, status, "plan_new", view{
		User: &user, Nav: "plans", Title: "New plan", Error: viewErr, Form: "plan", Data: d,
	})
}

// planKey returns the key of the amount message of the plan row form of category in
// currency.
func planKey(category int64, currency string) string {
	return "amount[" + strconv.FormatInt(category, 10) + ":" + currency + "]"
}

func (u *UI) setPlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f planForm
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	month, err := timex.ParseMonth(f.Month)
	if err != nil {
		return fmt.Errorf("month: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	amountKey := planKey(f.Category, f.Currency)
	if f.New {
		amountKey = "amount"
		if f.Category == 0 {
			errs["category"] = u.dicts.T(p.lang, "required")
		}
		if !money.ValidCurrency(f.Currency) {
			errs["currency"] = u.dicts.T(p.lang, "choose from the list")
		}
	}
	// an empty amount removes only the month's plan; "0" with repeating stops carrying
	amount := money.Amount{}
	f.Amount = strings.TrimSpace(f.Amount)
	switch {
	case f.Amount == "" && f.New:
		errs[amountKey] = u.dicts.T(p.lang, "required")
	case f.Amount == "":
		f.Repeating = false
	default:
		if amount, err = money.Parse(f.Amount); err != nil {
			errs[amountKey] = u.dicts.T(p.lang, "enter a number")
		} else if amount.IsNegative() {
			errs[amountKey] = u.dicts.T(p.lang, "must not be negative")
		}
	}
	if len(errs) > 0 && f.New {
		return u.renderNewPlan(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	if len(errs) > 0 {
		return u.renderPlans(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	err = u.svc.Plans.Set(r.Context(), user.ID, f.Category, month, f.Currency, amount, f.Repeating)
	// the new plan form shows an error on itself, a plan row gets it as a toast
	if isUserError(err) && f.New {
		return u.renderNewPlan(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	if f.New {
		redirect(w, r, "/plans?"+url.Values{"month": {f.Month}}.Encode())
		return nil
	}
	// a plan row saved in place stays open, as a redirect would close it; setting it
	// again on a resubmit is harmless
	return u.renderPlans(w, r, user, http.StatusOK, f, nil)
}

func (u *UI) fillPlans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var f struct {
		Month string `form:"month"`
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
	redirect(w, r, "/plans?"+url.Values{"month": {f.Month}}.Encode())
	return nil
}
