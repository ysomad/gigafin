package importer_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/tbank"
)

// _tbankAccountsJSON is a made-up answer of accounts_light.
const _tbankAccountsJSON = `[
	{"id": "1001", "name": "Black", "accountType": "Current", "currency": {"name": "RUB"},
	 "moneyAmount": {"value": 1500.25, "currency": {"name": "RUB"}}},
	{"id": "1002", "name": "Savings", "accountType": "Saving", "currency": {"name": "RUB"},
	 "moneyAmount": {"value": 20000}},
	{"id": "1003", "name": "Platinum", "accountType": "Credit", "currency": {"name": "RUB"},
	 "moneyAmount": {"value": 98000}, "creditLimit": {"value": 100000}},
	{"id": "1004", "name": "Автокредит", "accountType": "CashLoan", "currency": {"name": "RUB"},
	 "debtAmount": {"value": -50000.5}},
	{"id": "1005", "name": "Долями", "accountType": "BNPL"},
	{"id": "1006", "name": "Old", "accountType": "Current", "currency": {"name": "RUB"}, "hidden": true}
]`

func tbankAccounts(t *testing.T) []tbank.Account {
	t.Helper()
	var accs []tbank.Account
	require.NoError(t, json.Unmarshal([]byte(_tbankAccountsJSON), &accs))
	return accs
}

func TestTBankAccountTracked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tracked bool
		balance string
	}{
		{name: "Black", tracked: true, balance: "1500.25"},
		{name: "Savings", tracked: true, balance: "20000"},
		{name: "Platinum", tracked: true, balance: "-2000"},
		{name: "Автокредит", tracked: true, balance: "-50000.5"},
		{name: "Долями", balance: "0"},
		{name: "Old", balance: "0"},
	}
	accs := tbankAccounts(t)
	require.Len(t, accs, len(tests))
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := accs[i]
			require.Equal(t, tt.name, a.Name)
			assert.Equal(t, tt.tracked, a.Tracked())
			assert.Equal(t, tt.balance, a.Balance().String())
		})
	}
}

func TestFromTBank(t *testing.T) {
	t.Parallel()

	ms := func(s string) int64 {
		tm, err := time.Parse(time.DateTime, s)
		require.NoError(t, err)
		return tm.UnixMilli()
	}
	// the purchase is the first row of the CSV export in TestTBankParse, 18:48:38 MSK
	purchase := ms("2026-10-29 15:48:38") + 123
	opsJSON := fmt.Sprintf(`[
		{"id": "1", "account": "1001", "type": "Debit", "status": "OK", "description": "Самокат",
		 "mccString": "5411", "operationId": {"source": "PrimeTrxn"},
		 "operationTime": {"milliseconds": %d}, "debitingTime": {"milliseconds": %d},
		 "accountAmount": {"value": 1040.5, "currency": {"name": "RUB"}},
		 "category": {"name": "Другое"}, "spendingCategory": {"name": "Супермаркеты"}},
		{"id": "2", "account": "1001", "type": "Debit", "status": "FAILED", "description": "Яндекс Такси",
		 "operationTime": {"milliseconds": %d}, "accountAmount": {"value": 425}},
		{"id": "3", "account": "1001", "type": "Credit", "status": "OK", "description": "Кэшбэк",
		 "operationId": {"source": "LoyaltyBonus"}, "operationTime": {"milliseconds": %d},
		 "accountAmount": {"value": 0}},
		{"id": "4", "account": "1001", "type": "Debit", "status": "OK", "description": "Кофейня",
		 "operationId": {"source": "PrimeAuth"}, "operationTime": {"milliseconds": %d},
		 "accountAmount": {"value": 300}},
		{"id": "5", "account": "1005", "type": "Debit", "status": "OK", "description": "Магазин",
		 "operationTime": {"milliseconds": %d}, "accountAmount": {"value": 1000}},
		{"id": "6", "account": "1002", "type": "Credit", "status": "OK", "description": "ООО Ромашка",
		 "operationId": {"source": "PrimeTrxn"}, "operationTime": {"milliseconds": %d},
		 "debitingTime": {"milliseconds": %d}, "accountAmount": {"value": 5000},
		 "payment": {"comment": "аванс"}},
		{"id": "7", "account": "1003", "type": "Debit", "status": "OK", "description": "Аптека",
		 "operationId": {"source": "PrimeTrxn"}, "operationTime": {"milliseconds": %d},
		 "debitingTime": {"milliseconds": %d}, "accountAmount": {"value": 700}}
	]`, purchase, purchase, ms("2026-10-29 15:13:18"), ms("2026-10-29 16:00:00"), ms("2026-10-30 08:00:00"),
		ms("2026-10-30 09:00:00"), ms("2026-10-30 06:00:00"), ms("2026-10-30 06:00:00"),
		ms("2026-10-30 10:00:00"), ms("2026-10-30 10:00:00"))
	var ops []tbank.Operation
	require.NoError(t, json.Unmarshal([]byte(opsJSON), &ops))

	// Black and Savings are linked; Platinum is not
	res := importer.FromTBank(tbankAccounts(t), []string{"1001", "1002"}, nil, ops, time.UTC)
	require.Len(t, res.Rows, 2)
	require.Len(t, res.Skipped, 5)
	reasons := make([]string, len(res.Skipped))
	for i, r := range res.Skipped {
		reasons[i] = r.SkipReason
	}
	assert.Equal(t, []string{
		`status "FAILED"`, "zero amount", "not debited yet", "account not linked", "account not linked",
	}, reasons)

	r := res.Rows[0]
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, time.Date(2026, 10, 29, 15, 48, 38, 0, time.UTC), r.Time, "milliseconds dropped as in the export")
	assert.Equal(t, "-1040.5", r.Amount.String())
	assert.Equal(t, "RUB", r.Currency)
	assert.Equal(t, "Супермаркеты", r.Category, "the spending category is the one the export shows")
	assert.Equal(t, "Самокат", r.Payee)
	assert.Equal(t, "5411", r.MCC)
	assert.Equal(t, "b10ca987538b087579b70e12a71e5a42", r.Hash(domain.ImportSourceTBank, 0),
		"same hash as the export row, so synced and exported operations dedupe")

	r = res.Rows[1]
	assert.Equal(t, "Savings", r.Account)
	assert.Equal(t, "5000", r.Amount.String())
	assert.Equal(t, "Другое", r.Category, "uncategorized as in the export")
	assert.Equal(t, "аванс", r.Note, "the payment comment when there is no message")
}

func TestFromTBankMatchesExport(t *testing.T) {
	t.Parallel()

	csv := _tbankHeader + "\n" +
		`"Black";"*2581";"29.10.2026 18:48:38";"-1 040,50";"RUB";"-1 040,50";"RUB";"Ок";` +
		`"Супермаркеты";"";"5411";"Самокат";"";"0,00";"-1040,00";"10,00";"Да"` + "\n"
	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	exp, err := importer.Parse(domain.ImportSourceTBank, strings.NewReader(csv), moscow)
	require.NoError(t, err)

	op := tbank.Operation{Account: "1001", Type: tbank.OpDebit, Status: tbank.OpStatusOK, Description: "Самокат"}
	op.OperationTime.Milliseconds = time.Date(2026, 10, 29, 15, 48, 38, 999e6, time.UTC).UnixMilli()
	op.AccountAmount.Value = money.MustParse("1040.50")
	op.SpendingCategory.Name = "Супермаркеты"
	res := importer.FromTBank(tbankAccounts(t), []string{"1001"}, nil, []tbank.Operation{op}, moscow)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, domain.HashRows(domain.ImportSourceTBank, exp.Rows),
		domain.HashRows(domain.ImportSourceTBank, res.Rows))
}

func TestFromTBankTransferWithUnlinked(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	op := func(account, typ string, value string, at time.Time) tbank.Operation {
		o := tbank.Operation{Account: account, Type: typ, Status: tbank.OpStatusOK, Description: "Перевод"}
		o.OperationTime.Milliseconds = at.UnixMilli()
		o.AccountAmount.Value = money.MustParse(value)
		return o
	}
	ops := []tbank.Operation{
		op("1001", tbank.OpDebit, "1000", at),                    // Black to Savings
		op("1002", "Credit", "1000", at.Add(30*time.Second)),     // its other side
		op("1002", "Credit", "1000", at.Add(time.Minute)),        // the Black side is taken
		op("1002", "Credit", "500", at.Add(time.Hour)),           // income of Savings
		op("1003", "Credit", "1000", at.Add(10*time.Second)),     // Dollars, another currency
		op("1002", tbank.OpDebit, "1000", at.Add(5*time.Minute)), // too late for a transfer
	}

	// only Black is linked, Savings is mapped
	res := importer.FromTBank(tbankAccounts(t), []string{"1001"}, []string{"1002"}, ops, time.UTC)
	require.Len(t, res.Rows, 2)
	assert.Equal(t, "Black", res.Rows[0].Account)
	assert.Equal(t, "-1000", res.Rows[0].Amount.String())
	assert.Equal(t, "Savings", res.Rows[1].Account, "the other side of the transfer, to pair")
	assert.Equal(t, "1000", res.Rows[1].Amount.String())
	require.Len(t, res.Skipped, 4)
	for _, r := range res.Skipped {
		assert.Equal(t, "account not linked", r.SkipReason)
	}

	// Savings is not mapped either: the Black side stays, to import as an expense
	res = importer.FromTBank(tbankAccounts(t), []string{"1001"}, nil, ops, time.UTC)
	require.Len(t, res.Rows, 1)
	assert.Equal(t, "Black", res.Rows[0].Account)
	assert.Len(t, res.Skipped, 5)
}
