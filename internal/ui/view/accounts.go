package view

import "github.com/ysomad/gigafin/internal/domain"

type Accounts struct {
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

// Account is the account page: the account and the values of its forms.
type Account struct {
	Account domain.Account
	Kinds   []domain.AccountKind
	// Edit is the edit form.
	Edit Accounts
	// Target and Date are the set balance form.
	Target string `form:"balance"`
	Date   string `form:"date"`
}
