package ui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

type accountsData struct {
	Assets      []domain.Account
	Liabilities []domain.Account
	Currencies  []string
	Kinds       []domain.AccountKind
	Name        string `form:"name"`
	Currency    string `form:"currency"`
	Kind        string `form:"kind"`
	CreditLimit string `form:"credit_limit"`
	Balance     string `form:"balance"`
}

func (u *UI) renderAccounts(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, d accountsData, viewErr error,
) error {
	accounts, err := u.svc.Accounts.List(r.Context(), user.ID)
	if err != nil {
		return err
	}

	for _, a := range accounts {
		if a.Kind.Liability() {
			d.Liabilities = append(d.Liabilities, a)
		} else {
			d.Assets = append(d.Assets, a)
		}
	}

	return u.render(
		w,
		r,
		status,
		"accounts",
		view{User: &user, Nav: "settings", Title: "Accounts", Error: viewErr, Data: d},
	)
}

// renderNewAccount renders the new account form with the values of d.
func (u *UI) renderNewAccount(
	w http.ResponseWriter, r *http.Request, user domain.User, status int, d accountsData, viewErr error,
) error {
	d.Currencies = money.Currencies()
	d.Kinds = domain.AccountKinds()

	if d.Currency == "" {
		d.Currency = domain.DefaultCurrency
	}

	if d.Kind == "" {
		d.Kind = string(domain.AccountDebit)
	}

	return u.render(
		w,
		r,
		status,
		"account_new",
		view{User: &user, Nav: "settings", Title: "New account", Error: viewErr, Form: "account", Data: d},
	)
}

// accountInput validates the decoded account form, of a new account if create, and
// returns the messages of its invalid inputs translated to lang.
func (u *UI) accountInput(d accountsData, lang string, create bool) (domain.AccountInput, fieldsError) {
	errs := fieldsError{}
	in := domain.AccountInput{
		Name:     strings.TrimSpace(d.Name),
		Currency: d.Currency,
		Kind:     domain.AccountKind(d.Kind),
	}
	switch {
	case in.Name == "":
		errs["name"] = u.dicts.T(lang, "required")
	case utf8.RuneCountInString(in.Name) > domain.MaxTextLen:
		errs["name"] = u.dicts.T(lang, "too long")
	}
	if in.Kind != "" && !slices.Contains(domain.AccountKinds(), in.Kind) {
		errs["kind"] = u.dicts.T(lang, "choose from the list")
	}
	if create && !money.ValidCurrency(in.Currency) {
		errs["currency"] = u.dicts.T(lang, "choose from the list")
	}
	if d.CreditLimit == "" || in.Kind != domain.AccountCredit {
		return in, errs
	}
	var err error
	switch in.CreditLimit, err = money.Parse(d.CreditLimit); {
	case err != nil:
		errs["credit_limit"] = u.dicts.T(lang, "enter a number")
	case !in.CreditLimit.IsPositive():
		errs["credit_limit"] = u.dicts.T(lang, "must be positive")
	}
	return in, errs
}

func (u *UI) accounts(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderAccounts(w, r, user, http.StatusOK, accountsData{}, nil)
}

func (u *UI) newAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderNewAccount(w, r, user, http.StatusOK, accountsData{}, nil)
}

func (u *UI) createAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var d accountsData
	if err := u.formDecoder.Decode(&d, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	d.CreditLimit, d.Balance = strings.TrimSpace(d.CreditLimit), strings.TrimSpace(d.Balance)
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	in, errs := u.accountInput(d, p.lang, true)
	var balance money.Amount
	if d.Balance != "" {
		var err error
		if balance, err = money.Parse(d.Balance); err != nil {
			errs["balance"] = u.dicts.T(p.lang, "enter a number")
		}
	}
	if len(errs) > 0 {
		return u.renderNewAccount(w, r, user, http.StatusUnprocessableEntity, d, errs)
	}
	_, err := u.svc.Accounts.CreateWithBalance(r.Context(), user.ID, in, balance, u.svc.Today(user))
	if isUserError(err) {
		return u.renderNewAccount(w, r, user, http.StatusUnprocessableEntity, d, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/accounts")
	return nil
}

// accountData is the account page: the account and the values of its forms.
type accountData struct {
	Account domain.Account
	Kinds   []domain.AccountKind
	// Edit is the edit form.
	Edit accountsData
	// Target and Date are the set balance form.
	Target string `form:"balance"`
	Date   string `form:"date"`
}

// renderAccount renders the page of account id. A nil d shows the account's values in
// the forms, otherwise d holds the values of a form post that failed with viewErr; the
// edit form, if not the one posted, shows the account's values.
func (u *UI) renderAccount(
	w http.ResponseWriter, r *http.Request, user domain.User, id int64, status int, d *accountData, viewErr error,
) error {
	a, err := u.svc.Accounts.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	if d == nil {
		d = &accountData{}
	}
	// the balance input always posts with its form
	if !r.PostForm.Has("balance") {
		d.Target, d.Date = a.Balance.Fixed(a.Currency), timex.FormatDate(u.svc.Today(user))
	}
	// the kind select always posts
	if d.Edit.Kind == "" {
		d.Edit = accountsData{Name: a.Name, Kind: string(a.Kind)}
		if !a.CreditLimit.IsZero() {
			d.Edit.CreditLimit = a.CreditLimit.Fixed(a.Currency)
		}
	}
	d.Account, d.Kinds = a, domain.AccountKinds()
	form := "edit"
	if r.PostForm.Has("balance") {
		form = "balance"
	}
	return u.render(w, r, status, "account", view{
		User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{a.Name}, Error: viewErr, Form: form, Data: *d,
	})
}

func (u *UI) account(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	return u.renderAccount(w, r, user, id, http.StatusOK, nil, nil)
}

func (u *UI) updateAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var d accountsData
	if err := u.formDecoder.Decode(&d, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	d.CreditLimit = strings.TrimSpace(d.CreditLimit)
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	in, errs := u.accountInput(d, p.lang, false)
	if len(errs) > 0 {
		return u.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, &accountData{Edit: d}, errs)
	}
	err = u.svc.Accounts.Update(r.Context(), user.ID, id, in)
	if isUserError(err) {
		return u.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, &accountData{Edit: d}, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (u *UI) setBalance(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	var d accountData
	if err := u.formDecoder.Decode(&d, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	p, _ := r.Context().Value(prefsKey{}).(prefs) //nolint:errcheck // set by authed
	errs := fieldsError{}
	target, err := money.Parse(d.Target)
	if err != nil {
		errs["balance"] = u.dicts.T(p.lang, "enter a number")
	}
	var date time.Time
	if d.Date != "" {
		if date, err = timex.ParseDate(d.Date); err != nil {
			errs["date"] = u.dicts.T(p.lang, "must be YYYY-MM-DD")
		}
	}
	if len(errs) > 0 {
		return u.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, &d, errs)
	}
	_, err = u.svc.Accounts.SetBalance(r.Context(), user.ID, id, target, date)
	if isUserError(err) {
		return u.renderAccount(w, r, user, id, http.StatusUnprocessableEntity, &d, err)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (u *UI) archiveAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := r.ParseForm(); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	if err := u.svc.Accounts.Archive(r.Context(), user.ID, id, r.PostFormValue("archived") == "1"); err != nil {
		return err
	}
	redirect(w, r, "/accounts/"+r.PathValue("id"))
	return nil
}

func (u *UI) deleteAccount(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	if err := u.svc.Accounts.Delete(r.Context(), user.ID, id); err != nil {
		return err
	}
	redirect(w, r, "/accounts")
	return nil
}
