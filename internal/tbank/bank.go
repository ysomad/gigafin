package tbank

import (
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
)

// bankData converts the accounts and operations of the API: the tracked accounts with
// their balances less the holds among ops not yet debited, and the operations of
// those accounts that import, as the rows of a CSV export of the same operations, so
// both dedupe against each other. Failed operations, zero amounts and holds, which may
// still change, are left out. Times are returned in loc.
func bankData(accs []account, ops []operation, loc *time.Location) domain.BankData {
	data := domain.BankData{Records: make(map[string][]domain.Record)}
	byID := make(map[string]domain.BankAccount, len(accs))
	for _, a := range accs {
		if !a.isTracked() {
			continue
		}
		t := domain.BankAccount{
			ExternalID: a.ID, Name: a.Name, Currency: strings.ToUpper(a.Currency.Name),
			Kind: domain.AccountDebit, Balance: a.balance(),
		}
		switch a.AccountType {
		case _accountSaving:
			t.Kind = domain.AccountSavings
		case _accountCredit:
			t.Kind, t.CreditLimit = domain.AccountCredit, a.CreditLimit.Value
		case _accountCashLoan:
			t.Kind = domain.AccountLoan
		}
		byID[a.ID] = t
	}
	for i, op := range ops {
		t, ok := byID[op.Account]
		if !ok || op.Status != _opStatusOK || op.value().IsZero() {
			continue
		}
		if op.isPending() {
			t.Balance = t.Balance.Sub(op.value())
			byID[op.Account] = t
			continue
		}
		r := domain.Record{
			ExternalID: op.ID,
			Line:       i + 1,
			Account:    t.Name,
			Time:       op.at().Truncate(time.Second).In(loc),
			Amount:     op.value(),
			Currency:   t.Currency,
			Category:   op.SpendingCategory.Name,
			Payee:      strings.TrimSpace(op.Description),
			Note:       strings.TrimSpace(op.Message),
		}
		if r.Note == "" {
			r.Note = strings.TrimSpace(op.Payment.Comment)
		}
		if domain.ValidMCC(op.MCC) {
			r.MCC = op.MCC
		}
		data.Records[op.Account] = append(data.Records[op.Account], r)
	}
	for _, a := range accs {
		if t, ok := byID[a.ID]; ok {
			data.Accounts = append(data.Accounts, t)
		}
	}
	return data
}
