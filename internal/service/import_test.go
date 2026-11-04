package service_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

const _header = `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";` +
	`"Сумма в валюте счёта";"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";` +
	`"Описание";"Сообщение"`

func row(account, ts, amount, category, desc string) string {
	return `"` + account + `";"";"` + ts + `";"` + amount + `";"RUB";"` + amount + `";"RUB";"Ок";"` +
		category + `";"";"";"` + desc + `";""`
}

func file(rows ...string) *strings.Reader {
	return strings.NewReader(_header + "\n" + strings.Join(rows, "\n") + "\n")
}

type env struct {
	imp    *service.Imports
	fin    *service.Services
	userID int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	d := repotest.New(t)
	f := newServices(t, d)
	return env{imp: f.Imports, fin: f, userID: newUser(t, d)}
}

var _rows = []string{
	row("Black", "29.10.2026 18:48:38", "-1040,00", "Супермаркеты", "Самокат"),
	row("Black", "29.10.2026 18:48:38", "-1040,00", "Супермаркеты", "Самокат"), // same purchase twice
	row("Black", "29.10.2026 20:00:00", "-5000,00", "Переводы", "Между своими счетами"),
	row("Car loan", "29.10.2026 20:01:00", "5000,00", "Пополнения", "Между своими счетами"),
	row("Black", "30.10.2026 02:30:00", "300,00", "Супермаркеты", "Возврат Самокат"),
}

func TestImportFlow(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "tbank.csv", file(_rows...), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(5), im.RowsNew)
	assert.Equal(t, int64(0), im.RowsDup)

	p, err := e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	assert.False(t, p.Ready())
	require.Len(t, p.Accounts, 2)
	assert.Equal(t, domain.AccountDebit, p.Accounts[0].Kind)
	assert.Equal(t, domain.AccountLoan, p.Accounts[1].Kind, "guessed from the name")
	require.Len(t, p.Categories, 2, "paired transfer rows need no category")
	assert.Equal(t, domain.KindExpense, p.Categories[0].Kind)
	assert.Equal(t, domain.KindIncome, p.Categories[1].Kind, "refund maps per direction")
	assert.NotZero(t, p.Rows[2].PairID)
	assert.Equal(t, p.Rows[3].ID, p.Rows[2].PairID)

	_, err = e.imp.Commit(ctx, e.userID, im.ID, time.UTC)
	require.ErrorIs(t, err, errors.ErrConflict)

	existing, err := e.fin.Accounts.Create(ctx, e.userID, domain.AccountInput{Name: "Tinkoff Black", Currency: "RUB"})
	require.NoError(t, err)
	err = e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{
			{Name: "Black", Currency: "RUB", AccountID: existing.ID},
			{Name: "Car loan", Currency: "RUB", NewName: "Car loan", NewKind: domain.AccountLoan},
		},
		[]domain.CategoryChoice{
			{Name: "Супермаркеты", Kind: domain.KindExpense, Cat1: "Food", Cat2: "Groceries"},
			{Name: "Супермаркеты", Kind: domain.KindIncome, Cat1: "Refunds"},
		})
	require.NoError(t, err)

	p, err = e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	require.True(t, p.Ready())

	n, err := e.imp.Commit(ctx, e.userID, im.ID, time.FixedZone("UTC+7", 7*60*60))
	require.NoError(t, err)
	assert.Equal(t, 4, n, "2 expenses, 1 transfer, 1 income")

	ops, _, err := e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 4)
	assert.Equal(t, domain.OpIncome, ops[0].Type)
	assert.Equal(t, day(t, "2026-10-30"), ops[0].Date)
	assert.Equal(t, "06:30", ops[0].Time, "Moscow 02:30 is 06:30 in UTC+7")
	assert.Equal(t, domain.OpTransfer, ops[1].Type)
	assert.Equal(t, existing.ID, ops[1].AccountID)
	assert.Equal(t, "5000", ops[1].Amount.String())

	tree, err := e.fin.Categories.Tree(ctx, e.userID)
	require.NoError(t, err)
	names := domain.CategoryNames(tree)
	assert.Equal(t, "Refunds", names[ops[0].CategoryID], "no subcategory when cat2 is empty")
	i := slices.IndexFunc(tree, func(c domain.Category) bool { return c.Name == "Refunds" })
	require.NotEqual(t, -1, i)
	assert.Empty(t, tree[i].Children)

	accs, err := e.fin.Accounts.List(ctx, e.userID)
	require.NoError(t, err)
	balances := make(map[string]string, len(accs))
	for _, a := range accs {
		balances[a.Name] = a.Balance.String()
	}
	assert.Equal(t, map[string]string{"Tinkoff Black": "-6780", "Car loan": "5000"}, balances)

	// re-upload: everything is a duplicate, mappings are remembered
	again, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "tbank.csv", file(slices.Concat(_rows, []string{
		row("Black", "31.10.2026 10:00:00", "-100,00", "Супермаркеты", "Пятёрочка"),
	})...), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(5), again.RowsDup)
	assert.Equal(t, int64(1), again.RowsNew)
	p, err = e.imp.Preview(ctx, e.userID, again.ID)
	require.NoError(t, err)
	assert.True(t, p.Ready())

	// undo the first import: its operations go away and its rows become importable again
	deleted, err := e.imp.Undo(ctx, e.userID, im.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deleted)
	_, err = e.imp.Undo(ctx, e.userID, im.ID)
	require.ErrorIs(t, err, errors.ErrConflict)

	third, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "tbank.csv", file(_rows...), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(0), third.RowsDup)

	n, err = e.imp.Commit(ctx, e.userID, third.ID, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	require.NoError(t, e.imp.Discard(ctx, e.userID, again.ID))

	// a deleted imported operation stays imported, so it does not come back
	ops, _, err = e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.NoError(t, e.fin.Operations.Delete(ctx, e.userID, ops[0].ID))
	fourth, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "tbank.csv", file(_rows...), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(5), fourth.RowsDup)
}

func TestUnpairAndCurrencyMismatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "x.csv", file(_rows[2], _rows[3]), time.UTC)
	require.NoError(t, err)
	p, err := e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	assert.Empty(t, p.Categories)

	require.NoError(t, e.imp.Unpair(ctx, e.userID, im.ID, p.Rows[0].ID))
	p, err = e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	assert.Len(t, p.Categories, 2, "unpaired rows need categories")
	assert.Zero(t, p.Rows[1].PairID)

	usd, err := e.fin.Accounts.Create(ctx, e.userID, domain.AccountInput{Name: "USD", Currency: "USD"})
	require.NoError(t, err)
	err = e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{{Name: "Black", Currency: "RUB", AccountID: usd.ID}}, nil)
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestSkippedRows(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	failed := `"Black";"";"29.10.2026 18:00:00";"-10,00";"RUB";"-10,00";"RUB";"FAILED";"Такси";"";"";"Такси";""`
	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "x.csv", file(_rows[0], failed), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(1), im.RowsSkip)
	assert.Equal(t, int64(1), im.RowsNew)

	p, err := e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	require.Len(t, p.Skipped, 1)
	assert.Equal(t, `status "FAILED"`, p.Skipped[0].Row.SkipReason)
	assert.Equal(t, 3, p.Skipped[0].Row.Line)
	require.Len(t, p.Categories, 1, "skipped rows need no mapping")

	only, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceTBank, "y.csv", file(failed), time.UTC)
	require.NoError(t, err, "a file with only skipped rows still stages")
	assert.Equal(t, int64(1), only.RowsSkip)
	assert.Equal(t, int64(0), only.RowsNew)
}

func TestHashOccurrence(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	im, err := e.imp.Upload(
		t.Context(),
		e.userID,
		domain.ImportSourceTBank,
		"x.csv",
		file(_rows[0], _rows[1]),
		time.UTC,
	)
	require.NoError(t, err)
	p, err := e.imp.Preview(t.Context(), e.userID, im.ID)
	require.NoError(t, err)
	require.Len(t, p.Rows, 2)
	assert.NotEqual(t, p.Rows[0].Hash, p.Rows[1].Hash)
}

func TestImportZenMoney(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	csv := "date;categoryName;payee;comment;outcomeAccountName;outcome;outcomeCurrencyShortTitle;" +
		"incomeAccountName;income;incomeCurrencyShortTitle;createdDate;changedDate;qrCode\n" +
		`2026-10-29;"Transport / Taxi";"YANDEX";;"Black";"425";RUB;"Black";"0";RUB;"2026-10-29 22:13:18";;` + "\n" +
		`2026-10-27;;;;"Black";"24000";RUB;"Savings";"24000";RUB;"2026-10-27 21:59:38";"2026-10-27 22:00:27";` + "\n" +
		`2026-10-26;;;;"Black";"100";RUB;"Dollars";"1";USD;"2026-10-26 10:00:00";"2026-10-26 10:00:00";` + "\n"

	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceZenMoney, "zen.csv", strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, domain.ImportSourceZenMoney, im.Source)
	assert.Equal(t, int64(2), im.RowsNew)
	assert.Equal(t, int64(1), im.RowsSkip)

	p, err := e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	require.Len(t, p.Accounts, 2, "Black and the transfer destination Savings")
	require.Len(t, p.Categories, 1, "the transfer needs no category")
	assert.Zero(t, p.Rows[1].PairID)

	require.NoError(t, e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{
			{
				Name:           "Black",
				Currency:       "RUB",
				NewName:        "Black",
				NewKind:        domain.AccountCredit,
				NewCreditLimit: dec("50000"),
			},
			{Name: "Savings", Currency: "RUB", NewName: "Savings", NewKind: domain.AccountDebit},
		},
		[]domain.CategoryChoice{{Name: "Transport / Taxi", Kind: domain.KindExpense, Cat1: "Transport", Cat2: "Taxi"}}))
	n, err := e.imp.Commit(ctx, e.userID, im.ID, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	ops, _, err := e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 2)
	assert.Equal(t, domain.OpExpense, ops[0].Type)
	assert.Equal(t, "22:13", ops[0].Time)
	assert.Equal(t, domain.OpTransfer, ops[1].Type)
	assert.Equal(t, "24000", ops[1].Amount.String())
	assert.NotZero(t, ops[1].ToAccountID)

	black, err := e.fin.Accounts.Get(ctx, e.userID, ops[0].AccountID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountCredit, black.Kind)
	assert.Equal(t, "50000", black.CreditLimit.String())

	again, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceZenMoney, "zen.csv", strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(2), again.RowsDup)
	assert.Equal(t, int64(0), again.RowsNew)
}

func TestUploadUnknownSource(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	_, err := e.imp.Upload(t.Context(), e.userID, "sber", "x.csv", strings.NewReader("a"), time.UTC)
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestImportMCC(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	csv := "date;time;account;amount;currency;category;payee;mcc\n2026-10-29;18:48;Black;-1040;RUB;Food;SAMOKAT;5411\n"
	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceGigaFin, "x.csv", strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	require.NoError(t, e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{{Name: "Black", Currency: "RUB", NewName: "Black", NewKind: domain.AccountDebit}},
		[]domain.CategoryChoice{{Name: "Food", Kind: domain.KindExpense, Cat1: "Food"}}))
	_, err = e.imp.Commit(ctx, e.userID, im.ID, time.UTC)
	require.NoError(t, err)

	ops, _, err := e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 1)
	op := ops[0]
	assert.Equal(t, "5411", op.MCC)

	updated, err := e.fin.Operations.Update(ctx, e.userID, op.ID, domain.OperationInput{
		Type: op.Type, AccountID: op.AccountID, CategoryID: op.CategoryID, Amount: op.Amount, Date: op.Date,
		Payee: "Samokat",
	})
	require.NoError(t, err)
	assert.Equal(t, "Samokat", updated.Payee)
	assert.Equal(t, "5411", updated.MCC, "kept on update")
}

func TestImportSplits(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := t.Context()

	csv := "date;time;account;amount;currency;category;payee;origin_id\n" +
		"2026-10-16;10:48:30;Black;-40000;RUB;Home / Rent;Николай О.;1\n" +
		"2026-10-16;10:48:30;Black;-2150;RUB;Home / Utility;Николай О.;1\n" +
		"2026-10-17;12:00;Black;-5;RUB;Home / Rent;Lone;2\n"
	im, err := e.imp.Upload(ctx, e.userID, domain.ImportSourceGigaFin, "x.csv", strings.NewReader(csv), time.UTC)
	require.NoError(t, err)
	assert.Equal(t, int64(3), im.RowsNew)

	p, err := e.imp.Preview(ctx, e.userID, im.ID)
	require.NoError(t, err)
	require.Len(t, p.Categories, 2, "every split category is mapped")
	require.NoError(t, e.imp.SaveMapping(ctx, e.userID, im.ID,
		[]domain.AccountChoice{{Name: "Black", Currency: "RUB", NewName: "Black", NewKind: domain.AccountDebit}},
		[]domain.CategoryChoice{
			{Name: "Home / Rent", Kind: domain.KindExpense, Cat1: "Home", Cat2: "Rent"},
			{Name: "Home / Utility", Kind: domain.KindExpense, Cat1: "Home", Cat2: "Utility"},
		}))
	_, err = e.imp.Commit(ctx, e.userID, im.ID, time.UTC)
	require.NoError(t, err)

	ops, _, err := e.fin.Operations.List(ctx, e.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 3, "every split part is an operation, newest first")
	assert.Zero(t, ops[0].OriginID, "a group of one is not a split")
	assert.Equal(t, "2150", ops[1].Amount.String())
	assert.Equal(t, "40000", ops[2].Amount.String())
	assert.Equal(t, "Николай О.", ops[1].Payee)
	assert.NotEqual(t, ops[1].CategoryID, ops[2].CategoryID)
	assert.Equal(t, ops[2].ID, ops[1].OriginID)
	assert.Equal(t, ops[2].ID, ops[2].OriginID)
	assert.Equal(t, im.ID, ops[1].ImportID)

	n, err := e.imp.Undo(ctx, e.userID, im.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "undo deletes every part")
}

func TestImportLogs(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	d := repotest.New(t)
	var buf bytes.Buffer
	svc, err := service.New(d, timex.System{}, slog.New(slog.NewJSONHandler(&buf, nil)), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	userID := newUser(t, d)

	im, err := svc.Imports.Upload(ctx, userID, domain.ImportSourceTBank, "tbank.csv",
		file(row("Black", "29.10.2026 18:48:38", "-1040,00", "Супермаркеты", "Самокат")), time.UTC)
	require.NoError(t, err)
	err = svc.Imports.SaveMapping(ctx, userID, im.ID,
		[]domain.AccountChoice{{Name: "Black", Currency: "RUB", NewName: "Black", NewKind: domain.AccountDebit}},
		[]domain.CategoryChoice{{Name: "Супермаркеты", Kind: domain.KindExpense, Cat1: "Food"}})
	require.NoError(t, err)
	_, err = svc.Imports.Commit(ctx, userID, im.ID, time.UTC)
	require.NoError(t, err)

	var committed map[string]any
	for line := range strings.Lines(buf.String()) {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		if m["msg"] == "import committed" {
			committed = m
		}
	}
	require.NotNil(t, committed)
	assert.Equal(t, "INFO", committed["level"])
	assert.InDelta(t, userID, committed["user_id"], 0)
	assert.InDelta(t, im.ID, committed["import_id"], 0)
	assert.InDelta(t, 1, committed["created"], 0)
	for _, private := range []string{"1040", "Самокат", "Супермаркеты", "Black", "Food"} {
		assert.NotContains(t, buf.String(), private, "no amounts, payees or names in logs")
	}
}
