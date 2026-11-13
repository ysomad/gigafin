package accounts

import "github.com/ysomad/gigafin/internal/domain"

// accountsData is the accounts page.
type accountsData struct {
	Assets      []domain.Account
	Liabilities []domain.Account
}

// newAccountData is the new account page with the values of its form.
type newAccountData struct {
	Form       accountForm
	Currencies []string
	Kinds      []domain.AccountKind
}

// accountForm is decoded from the new account form and the edit form of an account,
// which has no currency and balance.
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
	Kinds   []domain.AccountKind
	Edit    accountForm
	Balance balanceForm
}
