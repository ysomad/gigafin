package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// AccountKind is what an account holds: money (an asset) or debt (a liability).
type AccountKind string

const (
	AccountCash  AccountKind = "cash"
	AccountDebit AccountKind = "debit"
	// AccountSavings is a deposit or a savings account.
	AccountSavings AccountKind = "savings"
	AccountCredit  AccountKind = "credit"
	AccountLoan    AccountKind = "loan"
)

// DefaultCurrency is the currency offered for a new account.
const DefaultCurrency = "RUB"

// AccountKinds returns all kinds, assets first.
func AccountKinds() []AccountKind {
	return []AccountKind{AccountCash, AccountDebit, AccountSavings, AccountCredit, AccountLoan}
}

// ParseAccountKind validates a kind string. Empty means debit.
func ParseAccountKind(s string) (AccountKind, error) {
	if s == "" {
		return AccountDebit, nil
	}
	k := AccountKind(s)
	if !slices.Contains(AccountKinds(), k) {
		return "", NewError(errors.ErrInvalid, "kind: unknown account kind %q", s)
	}
	return k, nil
}

// Liability reports whether the account holds debt.
func (k AccountKind) Liability() bool {
	return k == AccountCredit || k == AccountLoan
}

// Account is a money account in a single currency. Balance is signed for every kind:
// a credit card owing 500 has balance -500.
type Account struct {
	ID       int64
	Name     string
	Currency string
	Kind     AccountKind
	// CreditLimit is zero when not set. Only credit accounts have one.
	CreditLimit money.Amount
	Archived    bool
	Balance     money.Amount
}

// Owed returns the debt of a liability account, positive when money is owed.
func (a Account) Owed() money.Amount {
	return a.Balance.Neg()
}

// Available returns the credit left to spend. It is zero without a credit limit.
func (a Account) Available() money.Amount {
	if a.CreditLimit.IsZero() {
		return money.Amount{}
	}
	return a.CreditLimit.Add(a.Balance)
}

// AccountInput is a new or edited account. Currency cannot change after creation.
type AccountInput struct {
	Name     string
	Currency string
	Kind     AccountKind
	// CreditLimit is optional, zero means none. It is ignored unless Kind is credit,
	// so switching a card to debit does not trip over a leftover limit.
	CreditLimit money.Amount
}

// NewAccount validates input for a new account.
func NewAccount(in AccountInput) (Account, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Account{}, NewError(errors.ErrInvalid, "name: required")
	}
	if utf8.RuneCountInString(name) > _maxTextLen {
		return Account{}, NewError(errors.ErrInvalid, "name: too long")
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if !money.ValidCurrency(currency) {
		return Account{}, NewError(errors.ErrInvalid, "currency: unsupported currency %q", currency)
	}
	kind, err := ParseAccountKind(string(in.Kind))
	if err != nil {
		return Account{}, err
	}
	a := Account{Name: name, Currency: currency, Kind: kind}
	if in.CreditLimit.IsZero() || kind != AccountCredit {
		return a, nil
	}
	if !in.CreditLimit.IsPositive() {
		return Account{}, NewError(errors.ErrInvalid, "credit limit: must be positive")
	}
	if _, err := in.CreditLimit.ToMinor(currency); err != nil {
		return Account{}, fmt.Errorf("credit limit: %w", err)
	}
	a.CreditLimit = in.CreditLimit
	return a, nil
}

// Edit returns the account with the name, kind and credit limit of in. in.Currency is
// ignored.
func (a Account) Edit(in AccountInput) (Account, error) {
	in.Currency = a.Currency
	edited, err := NewAccount(in)
	if err != nil {
		return Account{}, err
	}
	edited.ID, edited.Archived, edited.Balance = a.ID, a.Archived, a.Balance
	return edited, nil
}

// BalanceAdjustment returns the operation that moves the account balance to target as
// of date, so a.Balance must be the balance at the end of date. ok is false when the
// balance already matches.
func (a Account) BalanceAdjustment(target money.Amount, date time.Time) (OperationInput, bool, error) {
	if _, err := target.ToMinor(a.Currency); err != nil {
		return OperationInput{}, false, fmt.Errorf("balance: %w", err)
	}
	delta := target.Sub(a.Balance)
	if delta.IsZero() {
		return OperationInput{}, false, nil
	}
	return OperationInput{
		Type:      OpAdjustment,
		AccountID: a.ID,
		Amount:    delta,
		Date:      date,
		Note:      "Balance set to " + target.Fixed(a.Currency),
	}, true, nil
}

// CheckDelete fails with errors.ErrConflict when the account has ops operations.
func (a Account) CheckDelete(ops int64) error {
	if ops > 0 {
		return NewError(errors.ErrConflict, "account has %d operations, archive it instead", ops)
	}
	return nil
}

// Total sums active accounts of one currency. Liabilities is positive when owed.
type Total struct {
	Currency    string
	Assets      money.Amount
	Liabilities money.Amount
}

// Net returns assets minus liabilities.
func (t Total) Net() money.Amount {
	return t.Assets.Sub(t.Liabilities)
}

// Totals sums active accounts by currency, ordered by currency.
func Totals(accounts []Account) []Total {
	byCur := make(map[string]Total)
	for _, a := range accounts {
		if a.Archived {
			continue
		}
		t := byCur[a.Currency]
		t.Currency = a.Currency
		if a.Kind.Liability() {
			t.Liabilities = t.Liabilities.Add(a.Owed())
		} else {
			t.Assets = t.Assets.Add(a.Balance)
		}
		byCur[a.Currency] = t
	}
	totals := make([]Total, 0, len(byCur))
	for _, t := range byCur {
		totals = append(totals, t)
	}
	slices.SortFunc(totals, func(a, b Total) int { return strings.Compare(a.Currency, b.Currency) })
	return totals
}

// AccountCurrencies returns the sorted currencies of active accounts, or every
// supported currency when there are none.
func AccountCurrencies(accounts []Account) []string {
	var out []string
	for _, a := range accounts {
		if !a.Archived && !slices.Contains(out, a.Currency) {
			out = append(out, a.Currency)
		}
	}
	if len(out) == 0 {
		return money.Currencies()
	}
	slices.Sort(out)
	return out
}
