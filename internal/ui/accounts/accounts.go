// Package accounts serves the accounts pages.
package accounts

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// Handler serves the accounts.
type Handler struct {
	app *kit.App
}

// New returns the handler of app.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Routes returns the routes of the accounts.
func (h *Handler) Routes() map[string]kit.Handler {
	return map[string]kit.Handler{
		"GET /accounts":               h.accounts,
		"POST /accounts":              h.createAccount,
		"GET /accounts/new":           h.newAccount,
		"GET /accounts/{id}":          h.account,
		"POST /accounts/{id}":         h.updateAccount,
		"POST /accounts/{id}/balance": h.setBalance,
		"POST /accounts/{id}/archive": h.archiveAccount,
		"POST /accounts/{id}/delete":  h.deleteAccount,
	}
}

func (h *Handler) accounts(w http.ResponseWriter, r *http.Request, user domain.User) error {
	ctx := r.Context()
	accounts, err := h.app.Svc.Accounts.List(ctx, user.ID)
	if err != nil {
		return err
	}
	bankAccounts, err := h.app.Svc.Banks.Accounts(ctx, user.ID)
	if err != nil {
		return err
	}
	d := accountsData{Banks: make(map[int64]domain.BankAccount, len(bankAccounts))}
	for _, t := range bankAccounts {
		if t.AccountID != 0 {
			d.Banks[t.AccountID] = t
		}
	}
	for _, a := range accounts {
		switch {
		case a.Archived:
			d.Archived = append(d.Archived, a)
		case a.Kind.Liability():
			d.Liabilities = append(d.Liabilities, a)
		default:
			d.Assets = append(d.Assets, a)
		}
	}

	return kit.Render(h.app, w, r, http.StatusOK, accountsPage, kit.Page{User: &user, Nav: "accounts", Title: "Accounts"},
		d, kit.Failure{})
}

// renderAccountForm renders the new account form with the values of f.
func (h *Handler) renderAccountForm(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, f accountForm, viewErr error,
) error {
	if f.Currency == "" {
		f.Currency = domain.DefaultCurrency
	}
	if f.Kind == "" {
		f.Kind = string(domain.AccountDebit)
	}
	d := newAccountData{Form: f, Currencies: money.Currencies(), Kinds: domain.AccountKinds()}

	return kit.Render(h.app, w, r, status, newAccountPage, kit.Page{User: &user, Nav: "accounts", Title: "New account"}, d,
		kit.Failure{Err: viewErr, Form: "account"})
}

// accountInput validates the decoded account form, of a new account if create, and
// returns the errors of its invalid inputs. The edit form has no kind, its credit
// limit is posted for credit accounts only.
func accountInput(d accountForm, create bool) (domain.AccountInput, kit.FieldsError) {
	errs := kit.FieldsError{}
	in := domain.AccountInput{
		Name:     strings.TrimSpace(d.Name),
		Currency: d.Currency,
		Kind:     domain.AccountKind(d.Kind),
	}
	switch {
	case in.Name == "":
		errs["name"] = domain.NewError(errors.ErrInvalid, "required")
	case utf8.RuneCountInString(in.Name) > domain.MaxTextLen:
		errs["name"] = domain.NewError(errors.ErrInvalid, "too long")
	}
	if in.Kind != "" && !slices.Contains(domain.AccountKinds(), in.Kind) {
		errs["kind"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if create && !money.ValidCurrency(in.Currency) {
		errs["currency"] = domain.NewError(errors.ErrInvalid, "choose from the list")
	}
	if d.CreditLimit == "" || create && in.Kind != domain.AccountCredit {
		return in, errs
	}
	var err error
	switch in.CreditLimit, err = money.Parse(d.CreditLimit); {
	case err != nil:
		errs["credit_limit"] = domain.NewError(errors.ErrInvalid, "enter a number")
	case !in.CreditLimit.IsPositive():
		errs["credit_limit"] = domain.NewError(errors.ErrInvalid, "must be positive")
	}
	return in, errs
}

func (h *Handler) newAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return h.renderAccountForm(w, r, user, http.StatusOK, accountForm{}, nil)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	var d accountForm
	if err := h.app.DecodeForm(r, &d); err != nil {
		return err
	}
	d.CreditLimit, d.Balance = strings.TrimSpace(d.CreditLimit), strings.TrimSpace(d.Balance)
	in, errs := accountInput(d, true)
	var balance money.Amount
	if d.Balance != "" {
		var err error
		if balance, err = money.Parse(d.Balance); err != nil {
			errs["balance"] = domain.NewError(errors.ErrInvalid, "enter a number")
		}
	}
	if len(errs) > 0 {
		return h.renderAccountForm(w, r, user, http.StatusUnprocessableEntity, d, errs)
	}
	_, err := h.app.Svc.Accounts.CreateWithBalance(r.Context(), user.ID, in, balance, h.app.Svc.Today(user))
	if kit.IsUserError(err) {
		return h.renderAccountForm(w, r, user, http.StatusUnprocessableEntity, d, err)
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/accounts")
	return nil
}

// renderAccount renders the page of account id with the values of d. The form named
// fail.Form, "edit" or "balance", if any, shows the values of its failed post, the other
// forms the account's values.
func (h *Handler) renderAccount(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, d accountData, fail kit.Failure,
) error {
	ctx := r.Context()
	a, err := h.app.Svc.Accounts.Get(ctx, user.ID, id)
	if err != nil {
		return err
	}
	bankAccounts, err := h.app.Svc.Banks.Accounts(ctx, user.ID)
	if err != nil {
		return err
	}
	if i := slices.IndexFunc(bankAccounts, func(t domain.BankAccount) bool { return t.AccountID == a.ID }); i >= 0 {
		d.Linked = bankAccounts[i]
	}
	if fail.Form != "edit" {
		d.Edit = accountForm{Name: a.Name}
		if !a.CreditLimit.IsZero() {
			d.Edit.CreditLimit = a.CreditLimit.Fixed(a.Currency)
		}
	}
	if fail.Form != "balance" {
		d.Balance = balanceForm{Target: a.Balance.Fixed(a.Currency), Date: timex.FormatDate(h.app.Svc.Today(user))}
	}
	// the first page of the operations, through the one the page is returned to at; the
	// operations list serves the next ones
	f := domain.Filter{AccountID: a.ID}
	var at int64
	if n, err := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64); err == nil {
		at = n
	}
	ops, next, err := h.app.Svc.Operations.ListThrough(ctx, user.ID, f, pagination.NewParams(pagination.Cursor{}, 20), at)
	if err != nil {
		return err
	}
	accounts, err := h.app.Svc.Accounts.List(ctx, user.ID)
	if err != nil {
		return err
	}
	tree, err := h.app.Svc.Categories.Tree(ctx, user.ID)
	if err != nil {
		return err
	}
	d.Days, d.CatNames, d.AccByID = kit.GroupByDay(ops), domain.CategoryShortNames(tree), domain.AccountsByID(accounts)
	if !next.IsZero() {
		d.MoreURL = fmt.Sprintf("/operations?account=%d&cursor=%s", a.ID, url.QueryEscape(next.String()))
	}
	d.Account = a
	return kit.Render(h.app, w, r, status, accountPage, kit.Page{
		User: &user, Nav: "accounts", Title: "%s", TitleArgs: []any{a.Name},
	}, d, fail)
}

func (h *Handler) account(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	return h.renderAccount(w, r, user, id, http.StatusOK, accountData{}, kit.Failure{})
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	var f accountForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	f.CreditLimit = strings.TrimSpace(f.CreditLimit)
	in, errs := accountInput(f, false)
	if len(errs) > 0 {
		return h.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, accountData{Edit: f},
			kit.Failure{Err: errs, Form: "edit"})
	}
	err = h.app.Svc.Accounts.Update(r.Context(), user.ID, id, in)
	if kit.IsUserError(err) {
		return h.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, accountData{Edit: f},
			kit.Failure{Err: err, Form: "edit"})
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (h *Handler) setBalance(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	var f balanceForm
	if err := h.app.DecodeForm(r, &f); err != nil {
		return err
	}
	errs := kit.FieldsError{}
	target, err := money.Parse(f.Target)
	if err != nil {
		errs["balance"] = domain.NewError(errors.ErrInvalid, "enter a number")
	}
	var date time.Time
	if f.Date != "" {
		if date, err = timex.ParseDate(f.Date); err != nil {
			errs["date"] = domain.NewError(errors.ErrInvalid, "must be YYYY-MM-DD")
		}
	}
	if len(errs) > 0 {
		return h.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, accountData{Balance: f},
			kit.Failure{Err: errs, Form: "balance"})
	}
	_, err = h.app.Svc.Accounts.SetBalance(r.Context(), user.ID, id, target, date)
	if kit.IsUserError(err) {
		return h.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, accountData{Balance: f},
			kit.Failure{Err: err, Form: "balance"})
	}
	if err != nil {
		return err
	}
	kit.Redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (h *Handler) archiveAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return domain.NewError(errors.ErrInvalid, "invalid form")
	}
	if err := h.app.Svc.Accounts.Archive(r.Context(), user.ID, id, r.PostFormValue("archived") == "1"); err != nil {
		return err
	}
	kit.Redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := kit.PathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.app.Svc.Accounts.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	kit.Redirect(w, r, "/accounts")
	return nil
}
