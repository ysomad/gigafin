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
		return in, domain.NewError(errors.ErrInvalid, "credit limit: enter a number")
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
			err = domain.NewError(errors.ErrInvalid, "opening balance: enter a number")
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

type accountData struct {
	Account domain.Account
	Kinds   []domain.AccountKind
	Today   string
	Target  string
}

func (u *UI) account(w http.ResponseWriter, r *http.Request, user domain.User) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return errors.ErrNotFound
	}
	a, err := u.svc.Accounts.Get(r.Context(), user.ID, id)
	if err != nil {
		return err
	}
	return u.render(w, r, http.StatusOK, "account", view{
		User: &user, Nav: "settings", Title: "%s", TitleArgs: []any{a.Name},
		Data: accountData{
			Account: a, Kinds: domain.AccountKinds(), Today: timex.FormatDate(u.svc.Today(user)),
			Target: a.Balance.Fixed(a.Currency),
		},
	})
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
	if err != nil {
		return err
	}
	if err := u.svc.Accounts.Update(r.Context(), user.ID, id, in); err != nil {
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
	var f struct {
		Balance string `form:"balance"`
		Date    string `form:"date"`
	}
	if err := u.formDecoder.Decode(&f, r.PostForm); err != nil {
		return fmt.Errorf("form: %w: %w", err, errors.ErrInvalid)
	}
	target, err := money.Parse(f.Balance)
	if err != nil {
		return domain.NewError(errors.ErrInvalid, "balance: enter a number")
	}
	var date time.Time
	if f.Date != "" {
		if date, err = timex.ParseDate(f.Date); err != nil {
			return fmt.Errorf("date: %w", err)
		}
	}
	if _, err := u.svc.Accounts.SetBalance(r.Context(), user.ID, id, target, date); err != nil {
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
