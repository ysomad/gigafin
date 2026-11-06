package ui

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		"accounts",
		view{User: &user, Nav: "settings", Title: "Accounts", Error: viewErr, Data: d},
	)
}

// accountInput converts the decoded account form.
func accountInput(d accountsData) (domain.AccountInput, error) {
	in := domain.AccountInput{
		Name:     d.Name,
		Currency: d.Currency,
		Kind:     domain.AccountKind(d.Kind),
	}
	if d.CreditLimit == "" {
		return in, nil
	}
	var err error
	if in.CreditLimit, err = money.Parse(d.CreditLimit); err != nil {
		return in, domain.NewFieldError(errors.ErrInvalid, "credit limit", "enter a number")
	}
	return in, nil
}

func (u *UI) accounts(w http.ResponseWriter, r *http.Request, user domain.User) error {
	return u.renderAccounts(w, r, user, http.StatusOK, accountsData{}, nil)
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
	in, err := accountInput(d)
	var balance money.Amount
	if err == nil && d.Balance != "" {
		if balance, err = money.Parse(d.Balance); err != nil {
			err = domain.NewFieldError(errors.ErrInvalid, "balance", "enter a number")
		}
	}
	if err == nil {
		_, err = u.svc.Accounts.CreateWithBalance(r.Context(), user.ID, in, balance, u.svc.Today(user))
	}
	if isUserError(err) {
		return u.renderAccounts(w, r, user, http.StatusUnprocessableEntity, d, err)
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
		d = &accountData{Target: a.Balance.Fixed(a.Currency), Date: timex.FormatDate(u.svc.Today(user))}
	}
	// the kind select always posts
	if d.Edit.Kind == "" {
		d.Edit = accountsData{Name: a.Name, Kind: string(a.Kind)}
		if !a.CreditLimit.IsZero() {
			d.Edit.CreditLimit = a.CreditLimit.Fixed(a.Currency)
		}
	}
	d.Account, d.Kinds = a, domain.AccountKinds()
	return u.render(w, r, status, "account", view{
		User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{a.Name}, Error: viewErr, Data: *d,
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
	in, err := accountInput(d)
	if err == nil {
		err = u.svc.Accounts.Update(r.Context(), user.ID, id, in)
	}
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
	var date time.Time
	target, err := money.Parse(d.Target)
	if err != nil {
		err = domain.NewFieldError(errors.ErrInvalid, "balance", "enter a number")
	} else if d.Date != "" {
		if date, err = timex.ParseDate(d.Date); err != nil {
			err = domain.NewFieldError(errors.ErrInvalid, "date", "must be YYYY-MM-DD")
		}
	}
	if err == nil {
		_, err = u.svc.Accounts.SetBalance(r.Context(), user.ID, id, target, date)
	}
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
