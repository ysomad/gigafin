package tbank_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/money"
)

// _accountsJSON is a made-up answer of accounts_light.
const _accountsJSON = `[
	{"id": "1001", "name": "Black", "accountType": "Current", "currency": {"name": "rub"},
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

// fetch returns what Fetch makes of the accounts of _accountsJSON and ops, the payload
// of operations.
func fetch(t *testing.T, ops string, loc *time.Location) domain.BankData {
	t.Helper()
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		payload := ops
		if r.URL.Path == "/v1/accounts_light" {
			payload = _accountsJSON
		}
		write(t, w, `{"resultCode": "OK", "payload": `+payload+`}`)
	})
	data, err := c.Fetch(t.Context(), encode(t, session{DeviceID: "DEVICE"}), time.Now(), time.Now(), loc)
	require.NoError(t, err)
	return data
}

func TestFetchAccounts(t *testing.T) {
	t.Parallel()

	ops := `[{"id": "1", "account": "1001", "type": "Debit", "status": "OK", "operationId": {"source": "PrimeAuth"},
		"operationTime": {"milliseconds": 1793836800123}, "accountAmount": {"value": 10.5}}]`
	data := fetch(t, ops, time.UTC)
	dec := money.MustParse
	assert.Equal(t, []domain.BankAccount{
		{ExternalID: "1001", Name: "Black", Currency: "RUB", Kind: domain.AccountDebit, Balance: dec("1510.75")},
		{ExternalID: "1002", Name: "Savings", Currency: "RUB", Kind: domain.AccountSavings, Balance: dec("20000")},
		{
			ExternalID: "1003", Name: "Platinum", Currency: "RUB", Kind: domain.AccountCredit, Balance: dec("-2000"),
			CreditLimit: dec("100000"),
		},
		{ExternalID: "1004", Name: "Автокредит", Currency: "RUB", Kind: domain.AccountLoan, Balance: dec("-50000.5")},
	}, data.Accounts, "without the installment plan and the hidden account; Black less the hold")
	assert.Empty(t, data.Records, "the hold is not imported")
}

func TestFetchRecords(t *testing.T) {
	t.Parallel()

	ms := func(s string) int64 {
		tm, err := time.Parse(time.DateTime, s)
		require.NoError(t, err)
		return tm.UnixMilli()
	}
	// the purchase is the first row of the CSV export in TestTBankParse, 18:48:38 MSK
	purchase := ms("2026-10-29 15:48:38") + 123
	ops := fmt.Sprintf(`[
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
		 "payment": {"comment": "аванс"}}
	]`, purchase, purchase, ms("2026-10-29 15:13:18"), ms("2026-10-29 16:00:00"), ms("2026-10-30 08:00:00"),
		ms("2026-10-30 09:00:00"), ms("2026-10-30 06:00:00"), ms("2026-10-30 06:00:00"))

	data := fetch(t, ops, time.UTC)
	require.Len(t, data.Records, 2, "failed, zero, the hold and the installment plan are left out")
	require.Len(t, data.Records["1001"], 1)
	r := data.Records["1001"][0]
	assert.Equal(t, "Black", r.Account)
	assert.Equal(t, time.Date(2026, 10, 29, 15, 48, 38, 0, time.UTC), r.Time, "milliseconds dropped as in the export")
	assert.Equal(t, "-1040.5", r.Amount.String())
	assert.Equal(t, "RUB", r.Currency)
	assert.Equal(t, "Супермаркеты", r.Category, "the spending category is the one the export shows")
	assert.Equal(t, "Самокат", r.Payee)
	assert.Equal(t, "5411", r.MCC)
	assert.Equal(t, "b10ca987538b087579b70e12a71e5a42", r.Hash(domain.ImportSourceTBank, 0),
		"same hash as the export row, so synced and exported operations dedupe")

	require.Len(t, data.Records["1002"], 1)
	r = data.Records["1002"][0]
	assert.Equal(t, "Savings", r.Account)
	assert.Equal(t, "5000", r.Amount.String())
	assert.Empty(t, r.Category, "uncategorized gets no category")
	assert.Equal(t, "аванс", r.Note, "the payment comment when there is no message")
}

func TestFetchMatchesExport(t *testing.T) {
	t.Parallel()

	csv := `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";` +
		`"Сумма в валюте счёта";"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";` +
		`"Описание";"Сообщение";"Округление";"Сумма операции с округлением";"Бонусы (включая кэшбэк)";` +
		`"Учёт в аналитике"` + "\n" +
		`"Black";"*2581";"29.10.2026 18:48:38";"-1 040,50";"RUB";"-1 040,50";"RUB";"Ок";` +
		`"Супермаркеты";"";"5411";"Самокат";"";"0,00";"-1040,00";"10,00";"Да"` + "\n"
	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)
	exp, err := importer.Parse(domain.ImportSourceTBank, strings.NewReader(csv), moscow)
	require.NoError(t, err)

	at := time.Date(2026, 10, 29, 15, 48, 38, 999e6, time.UTC).UnixMilli()
	data := fetch(t, fmt.Sprintf(`[{"id": "1", "account": "1001", "type": "Debit", "status": "OK",
		"description": "Самокат", "operationTime": {"milliseconds": %d}, "accountAmount": {"value": 1040.50},
		"spendingCategory": {"name": "Супермаркеты"}}]`, at), moscow)

	assert.Equal(t, domain.HashRows(domain.ImportSourceTBank, exp.Rows),
		domain.HashRows(domain.ImportSourceTBank, data.Records["1001"]))
}
