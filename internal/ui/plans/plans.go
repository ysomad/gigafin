// Package plans serves the plans pages.
package plans

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the plans.
type Handler struct {
	app *kit.App
}

// New returns the handler of app.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of the plans.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /plans":       h.plans,
		"POST /plans":      h.setPlan,
		"DELETE /plans":    h.deletePlan,
		"GET /plans/new":   h.newPlan,
		"POST /plans/fill": h.fillPlans,
	}
}

func (h *Handler) plans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	return h.renderPlans(w, r, user, http.StatusOK, planPost{Month: q.Get("month")}, nil)
}

// renderPlans renders the plans of the month of f, and the values of f, if it is a form
// post that failed with viewErr.
func (h *Handler) renderPlans(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f planPost, viewErr error,
) error {
	today := h.app.Svc.Today(user)
	month := timex.MonthOf(today)
	if f.Month != "" {
		var err error
		if month, err = timex.ParseMonth(f.Month); err != nil {
			return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), f.Month)
		}
	}
	// next month is open to plan ahead
	months, err := h.app.Svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(today).Next(), month)
	if err != nil {
		return err
	}
	d := plansData{
		Month:  month,
		Months: months,
		From:   timex.FormatDate(month.First()),
		To:     timex.FormatDate(month.Last()),
		Posted: f,
	}
	if f.Category != 0 && !f.New {
		d.Open = strconv.FormatInt(f.Category, 10) + ":" + f.Currency
	}
	// months is a contiguous range, latest first, always holding month
	d.Prev, d.Next = timex.Neighbors(months, month)
	reports, err := h.app.Svc.Plans.Report(r.Context(), user.ID, month)
	if err != nil {
		return err
	}
	for _, kind := range []domain.Kind{domain.KindExpense, domain.KindIncome} {
		rows := reports[kind]
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
	return kit.Render(h.app, w, r, status, plansPage, kit.Page{
		User: &user, Nav: "plans", Title: "Plans %s",
		TitleArgs: []any{kit.T(r.Context(), month.First().Format("January")) + " " + month.First().Format("2006")},
	}, d, kit.Failure{Err: viewErr, Form: "plan"})
}

func (h *Handler) newPlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	return h.renderPlanForm(w, r, user, http.StatusOK, planPost{
		New: true, Month: q.Get("month"), Repeating: true,
	}, nil)
}

// renderPlanForm renders the new plan form with the values of f, and viewErr of its
// failed post.
func (h *Handler) renderPlanForm(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f planPost, viewErr error,
) error {
	today := h.app.Svc.Today(user)
	d := planFormData{Month: timex.MonthOf(today), Posted: f}
	if f.Month != "" {
		var err error
		if d.Month, err = timex.ParseMonth(f.Month); err != nil {
			return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), f.Month)
		}
	}
	var err error
	if d.Categories, err = h.app.Svc.Categories.Tree(r.Context(), user.ID); err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}
	d.Currencies = domain.AccountCurrencies(accounts)
	d.Months, err = h.app.Svc.Operations.Months(r.Context(), user.ID, timex.MonthOf(today).Next(), d.Month)
	if err != nil {
		return err
	}
	return kit.Render(h.app, w, r, status, newPlanPage, kit.Page{User: &user, Nav: "plans", Title: "New plan"}, d,
		kit.Failure{Err: viewErr, Form: "plan"})
}

func (h *Handler) setPlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f planPost
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	month, err := timex.ParseMonth(f.Month)
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), f.Month)
	}
	errs := kit.FieldsError{}
	amountKey := planKey(f.Category, f.Currency)
	if f.New {
		amountKey = "amount"
		if f.Category == 0 {
			errs["category"] = domain.NewError(errors.ErrInvalid, "required")
		}
		if !money.ValidCurrency(f.Currency) {
			errs["currency"] = domain.NewError(errors.ErrInvalid, "choose from the list")
		}
	}
	amount := money.Amount{}
	f.Amount = strings.TrimSpace(f.Amount)
	if f.Amount == "" {
		errs[amountKey] = domain.NewError(errors.ErrInvalid, "required")
	} else if amount, err = money.Parse(f.Amount); err != nil {
		errs[amountKey] = domain.NewError(errors.ErrInvalid, "enter a number")
	} else if !amount.IsPositive() {
		errs[amountKey] = domain.NewError(errors.ErrInvalid, "must be positive")
	}
	if len(errs) > 0 && f.New {
		return h.renderPlanForm(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	if len(errs) > 0 {
		return h.renderPlans(w, r, user, http.StatusUnprocessableEntity, f, errs)
	}
	err = h.app.Svc.Plans.Set(r.Context(), user.ID, f.Category, month, f.Currency, amount, f.Repeating)
	// the new plan form shows an error on itself, a plan row gets it as a toast
	if kit.IsUserError(err) && f.New {
		return h.renderPlanForm(w, r, user, http.StatusUnprocessableEntity, f, err)
	}
	if err != nil {
		return err
	}
	if f.New {
		kit.Redirect(w, r, "/plans?"+url.Values{"month": {f.Month}}.Encode())
		return nil
	}
	// a plan row saved in place stays open, as a redirect would close it; setting it
	// again on a resubmit is harmless
	return h.renderPlans(w, r, user, http.StatusOK, f, nil)
}

// deletePlan removes the plan of a row for its month, taken from the query as a DELETE
// has no form body.
func (h *Handler) deletePlan(w http.ResponseWriter, r *http.Request, user domain.User) error {
	q := r.URL.Query()
	month, err := timex.ParseMonth(q.Get("month"))
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), q.Get("month"))
	}
	category, err := strconv.ParseInt(q.Get("category"), 10, 64)
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := h.app.Svc.Plans.Delete(r.Context(), user.ID, category, month, q.Get("currency")); err != nil {
		return err
	}
	return h.renderPlans(w, r, user, http.StatusOK, planPost{Month: q.Get("month")}, nil)
}

func (h *Handler) fillPlans(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var f struct {
		Month string `form:"month"`
	}
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	month, err := timex.ParseMonth(f.Month)
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "%s: %q must be YYYY-MM", domain.Msg("Month"), f.Month)
	}
	n, err := h.app.Svc.Plans.Fill(r.Context(), user.ID, month)
	if err != nil {
		return err
	}
	if n > 0 {
		h.app.SetNotice(w, kit.T(r.Context(), "Repeated %d plans", n))
	} else {
		h.app.SetNotice(w, kit.T(r.Context(), "Nothing to repeat"))
	}
	kit.Redirect(w, r, "/plans?"+url.Values{"month": {f.Month}}.Encode())
	return nil
}
