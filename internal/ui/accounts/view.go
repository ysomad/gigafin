package accounts

import (
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

// accountsData is the accounts page.
type accountsData struct {
	Assets      []domain.Account
	Liabilities []domain.Account
	// Archived are the archived accounts of both, shown collapsed.
	Archived []domain.Account
	// Banks are the bank accounts by the account they sync with.
	Banks map[int64]domain.BankAccount
}

// newAccountData is the new account page with the values of its form.
type newAccountData struct {
	Form       accountForm
	Currencies []string
	Kinds      []domain.AccountKind
}

// accountForm is decoded from the new account form and the edit form of an account,
// which has no currency, kind and balance.
type accountForm struct {
	Name        string `form:"name"`
	Currency    string `form:"currency"`
	Kind        string `form:"kind"`
	CreditLimit string `form:"credit_limit"`
	Balance     string `form:"balance"`
}

// balanceForm is decoded from the set balance form of an account.
type balanceForm struct {
	Target string `form:"balance"`
	Date   string `form:"date"`
}

// accountData is the account page: the account and the values of its forms.
type accountData struct {
	Account domain.Account
	Edit    accountForm
	Balance balanceForm
	// Linked is the bank account the account syncs with, zero when none.
	Linked domain.BankAccount
	// Days are the first page of the account's operations, MoreURL the next one.
	Days     []kit.DayGroup
	CatNames map[int64]string
	AccByID  map[int64]domain.Account
	MoreURL  string
}
