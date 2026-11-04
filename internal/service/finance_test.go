package service_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

var _logins atomic.Int64

func newUser(t *testing.T, store *repository.Store) int64 {
	t.Helper()
	login := "user" + strconv.FormatInt(_logins.Add(1), 10)
	u, err := store.Write().Users.Create(t.Context(), login, "x", "UTC", domain.RoleUser, time.Now())
	require.NoError(t, err)
	return u.ID
}

func newServices(t *testing.T, store *repository.Store) *service.Services {
	t.Helper()
	s, err := service.New(store, timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
		Import:  service.ImportOptions{LoanPattern: `(?i)loan`, CreditPattern: `(?i)credit`},
	})
	require.NoError(t, err)
	return s
}

func dec(s string) money.Amount {
	return money.MustParse(s)
}

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := timex.ParseDate(s)
	require.NoError(t, err)
	return d
}

func month(t *testing.T, s string) timex.Month {
	t.Helper()
	m, err := timex.ParseMonth(s)
	require.NoError(t, err)
	return m
}

type fixture struct {
	svc       *service.Services
	userID    int64
	card      domain.Account
	cash      domain.Account
	usd       domain.Account
	groceries domain.Category
	cafe      domain.Category
	salary    domain.Category
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	d := repotest.New(t)
	svc := newServices(t, d)
	uid := newUser(t, d)
	ctx := t.Context()

	f := fixture{svc: svc, userID: uid}
	var err error
	f.card, err = svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	f.cash, err = svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Cash", Currency: "rub"})
	require.NoError(t, err)
	f.usd, err = svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Dollars", Currency: "USD"})
	require.NoError(t, err)

	food, err := svc.Categories.Create(ctx, uid, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	f.groceries, err = svc.Categories.Create(ctx, uid, food.ID, "", "Groceries")
	require.NoError(t, err)
	f.cafe, err = svc.Categories.Create(ctx, uid, food.ID, "", "Cafe")
	require.NoError(t, err)
	work, err := svc.Categories.Create(ctx, uid, 0, domain.KindIncome, "Work")
	require.NoError(t, err)
	f.salary, err = svc.Categories.Create(ctx, uid, work.ID, domain.KindExpense, "Salary")
	require.NoError(t, err)
	return f
}

func (f fixture) balance(t *testing.T, id int64) string {
	t.Helper()
	a, err := f.svc.Accounts.Get(t.Context(), f.userID, id)
	require.NoError(t, err)
	return a.Balance.Fixed("RUB")
}

func TestCategoryKindInheritedAndTwoLevels(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	assert.Equal(t, domain.KindIncome, f.salary.Kind, "cat2 takes the parent kind")
	_, err := f.svc.Categories.Create(t.Context(), f.userID, f.groceries.ID, "", "Too deep")
	require.ErrorIs(t, err, errors.ErrInvalid)

	_, err = f.svc.Categories.Create(t.Context(), f.userID, 0, domain.KindExpense, "Food")
	require.ErrorIs(t, err, errors.ErrExists)
}

func TestSplitOperation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type:       domain.OpExpense,
		AccountID:  f.card.ID,
		CategoryID: f.groceries.ID,
		Amount:     dec("1040"),
		Date:       day(t, "2026-10-05"),
		Time:       "18:48:38",
		Payee:      "Market",
		Note:       "weekly",
	})
	require.NoError(t, err)
	assert.False(t, op.IsSplit())

	parts, err := f.svc.Operations.Split(ctx, f.userID, op.ID, []domain.SplitPart{
		{CategoryID: f.groceries.ID, Amount: dec("700.50")},
		{CategoryID: f.cafe.ID, Amount: dec("339.50"), Note: "coffee"},
	})
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, op.ID, parts[0].ID)
	assert.NotEqual(t, op.ID, parts[1].ID)
	for _, p := range parts {
		assert.Equal(t, op.ID, p.OriginID)
		assert.Equal(t, "18:48", p.Time)
		assert.Equal(t, "Market", p.Payee)
	}
	assert.Equal(t, "700.50", parts[0].Amount.Fixed("RUB"))
	assert.Equal(t, "weekly", parts[0].Note)
	assert.Equal(t, f.cafe.ID, parts[1].CategoryID)
	assert.Equal(t, "coffee", parts[1].Note)
	assert.Equal(t, "-1040.00", f.balance(t, f.card.ID))

	_, err = f.svc.Operations.Split(ctx, f.userID, op.ID, []domain.SplitPart{
		{CategoryID: f.groceries.ID, Amount: dec("700")}, {CategoryID: f.cafe.ID, Amount: dec("1")},
	})
	require.ErrorIs(t, err, errors.ErrInvalid, "parts must add up to the amount")

	// parts are independent operations
	op, err = f.svc.Operations.Update(ctx, f.userID, parts[1].ID, domain.OperationInput{
		Type:       domain.OpExpense,
		AccountID:  f.card.ID,
		CategoryID: f.cafe.ID,
		Amount:     dec("300"),
		Date:       day(t, "2026-10-06"),
	})
	require.NoError(t, err)
	assert.Equal(t, parts[0].ID, op.OriginID, "an edited part stays in its split")
	require.NoError(t, f.svc.Operations.Delete(ctx, f.userID, parts[0].ID))
	assert.Equal(t, "-300.00", f.balance(t, f.card.ID))

	ops, _, err := f.svc.Operations.List(ctx, f.userID, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.True(t, ops[0].IsSplit())
}

func TestOperationValidation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	tests := []struct {
		name string
		give domain.OperationInput
	}{
		{name: "no amount", give: domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"), CategoryID: f.groceries.ID,
		}},
		{name: "no category", give: domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"), Amount: dec("10"),
		}},
		{name: "income category on expense", give: domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
			CategoryID: f.salary.ID, Amount: dec("9"),
		}},
		{name: "too precise", give: domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
			CategoryID: f.groceries.ID, Amount: dec("9.001"),
		}},
		{name: "transfer across currencies", give: domain.OperationInput{
			Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.usd.ID, Date: day(t, "2026-10-01"), Amount: dec("10"),
		}},
		{name: "transfer to itself", give: domain.OperationInput{
			Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.card.ID, Date: day(t, "2026-10-01"), Amount: dec("10"),
		}},
		{name: "transfer with category", give: domain.OperationInput{
			Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.cash.ID, Date: day(t, "2026-10-01"), Amount: dec("10"),
			CategoryID: f.groceries.ID,
		}},
		{name: "no date", give: domain.OperationInput{
			Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.cash.ID, Amount: dec("10"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.svc.Operations.Create(t.Context(), f.userID, tt.give)
			require.ErrorIs(t, err, errors.ErrInvalid)
		})
	}
}

func TestTopLevelCategoryOnSplit(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
		CategoryID: f.groceries.ParentID, Amount: dec("10"),
	})
	require.NoError(t, err)
	parts, err := f.svc.Operations.Split(ctx, f.userID, op.ID, []domain.SplitPart{
		{CategoryID: f.groceries.ParentID, Amount: dec("9")},
		{CategoryID: f.cafe.ID, Amount: dec("1")},
	})
	require.NoError(t, err)
	assert.Equal(t, f.groceries.ParentID, parts[0].CategoryID)

	rows, _, err := f.svc.Plans.Report(ctx, f.userID, month(t, "2026-10"), domain.KindExpense)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "Food", rows[0].Category.Name)
	assert.Equal(t, "10", rows[0].Spent.String(), "cat1 counts its own operations and its children")
	assert.Equal(t, "Cafe", rows[1].Category.Name)
}

func TestBalancesWithTransferAndAdjustment(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	_, err := f.svc.Accounts.SetBalance(ctx, f.userID, f.card.ID, dec("1000"), day(t, "2026-10-01"))
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpIncome, AccountID: f.card.ID, Date: day(t, "2026-10-02"),
		CategoryID: f.salary.ID, Amount: dec("500"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.cash.ID, Date: day(t, "2026-10-03"), Amount: dec("300"),
	})
	require.NoError(t, err)

	assert.Equal(t, "1200.00", f.balance(t, f.card.ID))
	assert.Equal(t, "300.00", f.balance(t, f.cash.ID))

	op, err := f.svc.Accounts.SetBalance(ctx, f.userID, f.card.ID, dec("1150.5"), day(t, "2026-10-04"))
	require.NoError(t, err)
	assert.Equal(t, domain.OpAdjustment, op.Type)
	assert.Equal(t, "-49.50", op.Amount.Fixed("RUB"))
	assert.Equal(t, "1150.50", f.balance(t, f.card.ID))

	op, err = f.svc.Accounts.SetBalance(ctx, f.userID, f.card.ID, dec("1150.5"), day(t, "2026-10-04"))
	require.NoError(t, err)
	assert.Zero(t, op.ID, "no adjustment when balance matches")

	op, err = f.svc.Accounts.SetBalance(ctx, f.userID, f.card.ID, dec("900"), day(t, "2026-10-02"))
	require.NoError(t, err)
	assert.Equal(t, "-600.00", op.Amount.Fixed("RUB"), "from the balance at the end of the date")
	assert.Equal(t, "550.50", f.balance(t, f.card.ID), "later operations still count")

	err = f.svc.Accounts.Delete(ctx, f.userID, f.cash.ID)
	require.ErrorIs(t, err, errors.ErrConflict)
}

func TestListOperationsFiltersAndPages(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	for i := 1; i <= 5; i++ {
		cat := f.groceries.ID
		if i%2 == 0 {
			cat = f.cafe.ID
		}
		_, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-0"+strconv.Itoa(i)),
			Payee:      "shop " + strconv.Itoa(i),
			CategoryID: cat, Amount: dec("10"),
		})
		require.NoError(t, err)
	}
	_, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpTransfer, AccountID: f.cash.ID, ToAccountID: f.card.ID, Date: day(t, "2026-10-06"), Amount: dec("1"),
	})
	require.NoError(t, err)

	page, next, err := f.svc.Operations.List(ctx, f.userID, domain.Filter{AccountID: f.card.ID},
		pagination.Params{Limit: 4})
	require.NoError(t, err)
	require.Len(t, page, 4)
	require.NotZero(t, next)
	assert.Equal(t, domain.OpTransfer, page[0].Type, "incoming transfer matches the account filter")

	page, next, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{AccountID: f.card.ID},
		pagination.Params{Limit: 4, After: next})
	require.NoError(t, err)
	require.Len(t, page, 2)
	assert.Zero(t, next)
	assert.Equal(t, day(t, "2026-10-01"), page[1].Date)

	page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{CategoryID: f.cafe.ParentID}, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, page, 5, "cat1 filter matches its children")

	page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{CategoryID: f.cafe.ID}, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, page, 2)

	page, _, err = f.svc.Operations.List(ctx, f.userID,
		domain.Filter{Query: "shop 3", DateFrom: day(t, "2026-10-02")}, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, page, 1)

	op, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
		Payee: "ПЯТЁРОЧКА", Note: "молоко", CategoryID: f.groceries.ID, Amount: dec("10"),
	})
	require.NoError(t, err)
	for _, q := range []string{"пятерочка", "пят мол", `"Пятёр`} {
		page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{Query: q}, pagination.Params{})
		require.NoError(t, err, q)
		require.Len(t, page, 1, q)
		assert.Equal(t, op.ID, page[0].ID, q)
	}
	for _, q := range []string{"олоко", "пят хлеб", "50%", "AND", "*"} {
		page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{Query: q}, pagination.Params{})
		require.NoError(t, err, q)
		assert.Empty(t, page, q)
	}
	in := domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
		Payee: "Магнит", CategoryID: f.groceries.ID, Amount: dec("10"),
	}
	_, err = f.svc.Operations.Update(ctx, f.userID, op.ID, in)
	require.NoError(t, err)
	page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{Query: "магнит"}, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, page, 1, "index follows updates")
	require.NoError(t, f.svc.Operations.Delete(ctx, f.userID, op.ID))
	page, _, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{Query: "магнит"}, pagination.Params{})
	require.NoError(t, err)
	assert.Empty(t, page, "and deletes")

	_, next, err = f.svc.Operations.List(ctx, f.userID, domain.Filter{}, pagination.Params{Limit: 1})
	require.NoError(t, err)
	require.NotZero(t, next)
	assert.Equal(t, day(t, "2026-10-06"), next.Date)
}

func TestPlanReport(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	_, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-05"),
		CategoryID: f.groceries.ID, Amount: dec("300"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-05"),
		CategoryID: f.cafe.ID, Amount: dec("200"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-11-01"),
		CategoryID: f.cafe.ID, Amount: dec("999"),
	})
	require.NoError(t, err)

	require.NoError(t, f.svc.Plans.Set(
		ctx, f.userID, f.groceries.ParentID, month(t, "2026-10"), "RUB", dec("1000"), true /* repeating */))
	require.NoError(t, f.svc.Plans.Set(
		ctx, f.userID, f.cafe.ID, month(t, "2026-10"), "RUB", dec("150"), true /* repeating */))

	rows, _, err := f.svc.Plans.Report(ctx, f.userID, month(t, "2026-10"), domain.KindExpense)
	require.NoError(t, err)
	require.Len(t, rows, 3)

	assert.Equal(t, "Food", rows[0].Category.Name)
	assert.Equal(t, "500", rows[0].Spent.String())
	assert.Equal(t, int64(50), rows[0].Percent())

	assert.Equal(t, "Groceries", rows[1].Category.Name, "more spent goes first")
	assert.False(t, rows[1].HasPlan)

	assert.Equal(t, "Cafe", rows[2].Category.Name)
	assert.Equal(t, "-50", rows[2].Left().String())

	n, err := f.svc.Plans.Fill(ctx, f.userID, month(t, "2026-11"))
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	require.NoError(t, f.svc.Plans.Set(
		ctx, f.userID, f.cafe.ID, month(t, "2026-11"), "RUB", money.Amount{}, false /* repeating */))
	plans, err := f.svc.Plans.List(ctx, f.userID, month(t, "2026-11"))
	require.NoError(t, err)
	assert.Len(t, plans, 1)
}

func TestPlanCarry(t *testing.T) {
	t.Parallel()

	clock := new(timex.Fixed(time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)))
	store := repotest.New(t)
	svc, err := service.New(store, clock, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	uid := newUser(t, store)
	ctx := t.Context()
	food, err := svc.Categories.Create(ctx, uid, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	cafe, err := svc.Categories.Create(ctx, uid, 0, domain.KindExpense, "Cafe")
	require.NoError(t, err)

	amounts := func(m string) map[int64]string {
		t.Helper()
		plans, err := svc.Plans.List(ctx, uid, month(t, m))
		require.NoError(t, err)
		out := make(map[int64]string, len(plans))
		for _, b := range plans {
			out[b.CategoryID] = b.Amount.String()
		}
		return out
	}

	require.NoError(t, svc.Plans.Set(ctx, uid, food.ID, month(t, "2026-10"), "RUB", dec("10000"), true /* repeating */))
	require.NoError(t, svc.Plans.Set(ctx, uid, cafe.ID, month(t, "2026-10"), "RUB", dec("500"), true /* repeating */))
	// a change of one month is not carried
	require.NoError(t, svc.Plans.Set(ctx, uid, food.ID, month(t, "2026-11"), "RUB", dec("15000"), false /* repeating */))
	// a zero repeating plan stops carrying
	require.NoError(t, svc.Plans.Set(
		ctx, uid, cafe.ID, month(t, "2026-11"), "RUB", money.Amount{}, true /* repeating */))
	assert.Equal(t, map[int64]string{food.ID: "15000"}, amounts("2026-11"), "zero plan is not listed")

	n, err := svc.Plans.CarryAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, map[int64]string{food.ID: "10000"}, amounts("2026-12"))

	// a plan removed after the carry stays removed
	require.NoError(t, svc.Plans.Set(
		ctx, uid, food.ID, month(t, "2026-12"), "RUB", money.Amount{}, false /* repeating */))
	n, err = svc.Plans.CarryAll(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, amounts("2026-12"))

	// the manual fill ignores carried months
	n, err = svc.Plans.Fill(ctx, uid, month(t, "2026-12"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	*clock = timex.Fixed(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	n, err = svc.Plans.CarryAll(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, map[int64]string{food.ID: "10000"}, amounts("2027-01"))
}

func TestDashboard(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	_, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpIncome, AccountID: f.card.ID, Date: day(t, "2026-10-01"),
		CategoryID: f.salary.ID, Amount: dec("1000"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-05"),
		CategoryID: f.groceries.ID, Amount: dec("300"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-05"),
		CategoryID: f.cafe.ID, Amount: dec("100"),
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2025-10-31"),
		CategoryID: f.cafe.ID, Amount: dec("999"),
	})
	require.NoError(t, err)

	d, err := f.svc.Dashboard.Get(ctx, f.userID, month(t, "2026-10"), month(t, "2026-09"))
	require.NoError(t, err)

	require.Len(t, d.Totals, 2)
	assert.Equal(t, "-399", d.Totals[0].Net().String(), "balance is all time")

	require.Len(t, d.Expenses, 1)
	assert.Equal(t, "Food", d.Expenses[0].Category.Name)
	assert.Equal(t, "400", d.Expenses[0].Spent.String())

	require.Len(t, d.Trends, 1, "operations outside the year are left out")
	require.Len(t, d.Trends[0].Months, 12)
	assert.Equal(t, "2025-11", d.Trends[0].Months[0].Month.String())
	last := d.Trends[0].Last()
	assert.Equal(t, "1000", last.Income.String())
	assert.Equal(t, "400", last.Expense.String())
}

func TestOperationMonths(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	months, err := f.svc.Operations.Months(ctx, f.userID)
	require.NoError(t, err)
	assert.Empty(t, months, "no operations")

	months, err = f.svc.Operations.Months(ctx, f.userID, month(t, "2026-11"))
	require.NoError(t, err)
	require.Len(t, months, 1)
	assert.Equal(t, "2026-11", months[0].String())

	for _, date := range []string{"2026-10-05", "2025-12-31"} {
		_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, date),
			CategoryID: f.cafe.ID, Amount: dec("100"),
		})
		require.NoError(t, err)
	}

	months, err = f.svc.Operations.Months(ctx, f.userID)
	require.NoError(t, err)
	require.Len(t, months, 11, "first to last operation")
	assert.Equal(t, "2026-10", months[0].String(), "latest first")
	assert.Equal(t, "2025-12", months[10].String())

	months, err = f.svc.Operations.Months(ctx, f.userID, month(t, "2027-01"), month(t, "2026-01"))
	require.NoError(t, err)
	require.Len(t, months, 14, "widened to include")
	assert.Equal(t, "2027-01", months[0].String())
	assert.Equal(t, "2025-12", months[13].String())
}

func TestTags(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	trip, err := f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"), CategoryID: f.cafe.ID,
		Amount: dec("30"), Tags: []string{"Vacation 2026", "food"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Vacation 2026", "food"}, trip.Tags)
	assert.True(t, trip.UpdatedAt.IsZero(), "not set on create")
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpIncome, AccountID: f.card.ID, Date: day(t, "2026-10-02"), CategoryID: f.salary.ID,
		Amount: dec("100"), Tags: []string{"vacation 2026"},
	})
	require.NoError(t, err)
	_, err = f.svc.Operations.Create(ctx, f.userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-03"), CategoryID: f.cafe.ID, Amount: dec("7"),
	})
	require.NoError(t, err)

	tags, err := f.svc.Operations.Tags(ctx, f.userID)
	require.NoError(t, err)
	assert.Equal(t, []string{"Vacation 2026", "food"}, tags, "a case variant joins the tag")

	vacation := domain.Filter{Tag: "Vacation 2026"}
	ops, _, err := f.svc.Operations.List(ctx, f.userID, vacation, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, ops, 2)
	totals, err := f.svc.Operations.Totals(ctx, f.userID, vacation)
	require.NoError(t, err)
	require.Len(t, totals, 1)
	assert.Equal(t, "30", totals[0].Expense.String())
	assert.Equal(t, "70", totals[0].Net().String())

	parts, err := f.svc.Operations.Split(ctx, f.userID, trip.ID, []domain.SplitPart{
		{CategoryID: f.cafe.ID, Amount: dec("20")}, {CategoryID: f.groceries.ID, Amount: dec("10")},
	})
	require.NoError(t, err)
	require.Len(t, parts, 2)
	assert.Equal(t, trip.Tags, parts[1].Tags, "parts keep the tags")

	_, err = f.svc.Operations.Update(ctx, f.userID, parts[0].ID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"), CategoryID: f.cafe.ID, Amount: dec("20"),
	})
	require.NoError(t, err)
	require.NoError(t, f.svc.Operations.Delete(ctx, f.userID, parts[1].ID))
	tags, err = f.svc.Operations.Tags(ctx, f.userID)
	require.NoError(t, err)
	assert.Equal(t, []string{"Vacation 2026"}, tags, "a tag no operation has is deleted")
}

func TestListByAmount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	for _, in := range []domain.OperationInput{
		{Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-01"), CategoryID: f.cafe.ID, Amount: dec("2.1")},
		{Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-02"), CategoryID: f.cafe.ID, Amount: dec("2")},
		{Type: domain.OpExpense, AccountID: f.usd.ID, Date: day(t, "2026-10-03"), CategoryID: f.cafe.ID, Amount: dec("500")},
		{Type: domain.OpAdjustment, AccountID: f.cash.ID, Date: day(t, "2026-10-04"), Amount: dec("-3")},
	} {
		_, err := f.svc.Operations.Create(ctx, f.userID, in)
		require.NoError(t, err)
	}
	amounts := func(fl domain.Filter) []string {
		t.Helper()
		ops, _, err := f.svc.Operations.List(ctx, f.userID, fl, pagination.Params{})
		require.NoError(t, err)
		out := make([]string, len(ops))
		for i, op := range ops {
			out[i] = op.Amount.String()
		}
		return out
	}

	assert.Equal(t, []string{"-3", "2.1"},
		amounts(domain.Filter{
			AmountMin: money.NullAmount{Amount: dec("2.005"), Valid: true},
			AmountMax: money.NullAmount{Amount: dec("3"), Valid: true},
		}),
		"min rounds up to the currency, adjustments by absolute amount")
	assert.Equal(t, []string{"500"}, amounts(domain.Filter{AmountMin: money.NullAmount{Amount: dec("100"), Valid: true}}),
		"any currency")

	_, _, err := f.svc.Operations.List(ctx, f.userID, domain.Filter{
		AmountMin: money.NullAmount{Amount: dec("5"), Valid: true},
		AmountMax: money.NullAmount{Amount: dec("1"), Valid: true},
	}, pagination.Params{})
	require.ErrorIs(t, err, errors.ErrInvalid)
}

func TestDashboardReports(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	ctx := t.Context()

	for _, in := range []domain.OperationInput{
		{Type: domain.OpIncome, AccountID: f.usd.ID, Date: day(t, "2025-01-01"), CategoryID: f.salary.ID, Amount: dec("10")},
		{
			Type: domain.OpIncome, AccountID: f.card.ID, Date: day(t, "2026-09-01"),
			CategoryID: f.salary.ID, Amount: dec("1000"),
		},
		{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-09-10"),
			CategoryID: f.groceries.ID, Amount: dec("200"), Payee: "Shop",
		},
		{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-05"),
			CategoryID: f.groceries.ID, Amount: dec("300"), Payee: "Shop",
		},
		{
			Type: domain.OpExpense, AccountID: f.card.ID, Date: day(t, "2026-10-06"),
			CategoryID: f.cafe.ID, Amount: dec("100"), Payee: "Bar",
		},
		{
			Type: domain.OpTransfer, AccountID: f.card.ID, ToAccountID: f.cash.ID,
			Date: day(t, "2026-10-02"), Amount: dec("50"),
		},
	} {
		_, err := f.svc.Operations.Create(ctx, f.userID, in)
		require.NoError(t, err)
	}

	d, err := f.svc.Dashboard.Get(ctx, f.userID, month(t, "2026-10"), month(t, "2026-09"))
	require.NoError(t, err)

	require.Len(t, d.Expenses, 1)
	assert.Equal(t, "400", d.Expenses[0].Spent.String())
	assert.Equal(t, "200", d.Expenses[0].Prev.String())
	assert.Equal(t, int64(100), d.Expenses[0].Change())

	require.Len(t, d.Payees, 2)
	assert.Equal(t, "Shop", d.Payees[0].Payee)
	assert.Equal(t, "300", d.Payees[0].Total.String())
	assert.Equal(t, int64(1), d.Payees[0].Count)
	assert.Equal(t, "Bar", d.Payees[1].Payee)

	require.Len(t, d.TrendMonths, 12)
	require.Len(t, d.CategoryTrends, 1)
	food := d.CategoryTrends[0]
	assert.Equal(t, "Food", food.Category.Name)
	assert.Equal(t, "200", food.Months[10].String())
	assert.Equal(t, "400", food.Months[11].String())
	assert.Equal(t, "600", food.Total.String())

	require.Len(t, d.NetWorth, 2)
	rub := d.NetWorth[0]
	assert.Equal(t, "RUB", rub.Currency)
	assert.True(t, rub.Months[0].Balance.IsZero())
	assert.Equal(t, "800", rub.Months[10].Balance.String())
	assert.Equal(t, "400", rub.Months[11].Balance.String(), "a transfer between own accounts nets out")
	assert.Equal(t, "USD", d.NetWorth[1].Currency)
	assert.Equal(t, "10", d.NetWorth[1].Months[0].Balance.String(), "balance before the year counts")
}

func TestUserIsolation(t *testing.T) {
	t.Parallel()

	d := repotest.New(t)
	svc := newServices(t, d)
	ctx := t.Context()
	u, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	alice, bob := u.ID, newUser(t, d)

	acc, err := svc.Accounts.Create(ctx, alice, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	food, err := svc.Categories.Create(ctx, alice, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	groceries, err := svc.Categories.Create(ctx, alice, food.ID, "", "Groceries")
	require.NoError(t, err)
	op, err := svc.Operations.Create(ctx, alice, domain.OperationInput{
		Type: domain.OpExpense, AccountID: acc.ID, Date: day(t, "2026-10-01"),
		CategoryID: groceries.ID, Amount: dec("1"),
	})
	require.NoError(t, err)

	_, err = svc.Accounts.Get(ctx, bob, acc.ID)
	require.ErrorIs(t, err, errors.ErrNotFound)
	_, err = svc.Operations.Get(ctx, bob, op.ID)
	require.ErrorIs(t, err, errors.ErrNotFound)
	require.ErrorIs(t, svc.Operations.Delete(ctx, bob, op.ID), errors.ErrNotFound)

	bobAcc, err := svc.Accounts.Create(ctx, bob, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err, "names are unique per user")
	_, err = svc.Operations.Create(ctx, bob, domain.OperationInput{
		Type: domain.OpExpense, AccountID: bobAcc.ID, Date: day(t, "2026-10-01"),
		CategoryID: groceries.ID, Amount: dec("1"),
	})
	require.ErrorIs(t, err, errors.ErrInvalid, "cannot use another user's category")

	ops, _, err := svc.Operations.List(ctx, bob, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	assert.Empty(t, ops)
}

func TestDeleteAllData(t *testing.T) {
	t.Parallel()

	d := repotest.New(t)
	svc := newServices(t, d)
	imp := svc.Imports
	ctx := t.Context()
	u, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	alice, bob := u.ID, newUser(t, d)

	seed := func(uid int64) {
		card, err := svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Card", Currency: "RUB"})
		require.NoError(t, err)
		cash, err := svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Cash", Currency: "RUB"})
		require.NoError(t, err)
		food, err := svc.Categories.Create(ctx, uid, 0, domain.KindExpense, "Food")
		require.NoError(t, err)
		cafe, err := svc.Categories.Create(ctx, uid, food.ID, "", "Cafe")
		require.NoError(t, err)
		op, err := svc.Operations.Create(ctx, uid, domain.OperationInput{
			Type: domain.OpExpense, AccountID: card.ID, Date: day(t, "2026-10-01"), CategoryID: food.ID, Amount: dec("3"),
		})
		require.NoError(t, err)
		_, err = svc.Operations.Split(ctx, uid, op.ID, []domain.SplitPart{
			{CategoryID: food.ID, Amount: dec("1")}, {CategoryID: cafe.ID, Amount: dec("2")},
		})
		require.NoError(t, err)
		_, err = svc.Operations.Create(ctx, uid, domain.OperationInput{
			Type: domain.OpTransfer, AccountID: card.ID, ToAccountID: cash.ID, Date: day(t, "2026-10-02"), Amount: dec("5"),
		})
		require.NoError(t, err)
		require.NoError(t, svc.Plans.Set(ctx, uid, food.ID, month(t, "2026-10"), "RUB", dec("100"), false /* repeating */))

		csv := `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";` +
			`"Сумма в валюте счёта";"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";` +
			`"Описание";"Сообщение"` + "\n" +
			`"Black";"";"29.10.2026 18:48:38";"-10,00";"RUB";"-10,00";"RUB";"Ок";"Такси";"";"";"Такси";""` + "\n"
		im, err := imp.Upload(ctx, uid, domain.ImportSourceTBank, "x.csv", strings.NewReader(csv), time.UTC)
		require.NoError(t, err)
		require.NoError(t, imp.SaveMapping(ctx, uid, im.ID,
			[]domain.AccountChoice{{Name: "Black", Currency: "RUB", AccountID: card.ID}},
			[]domain.CategoryChoice{{Name: "Такси", Kind: domain.KindExpense, Cat1: "Transport", Cat2: "Taxi"}}))
		_, err = imp.Commit(ctx, uid, im.ID, time.UTC)
		require.NoError(t, err)
	}
	seed(alice)
	seed(bob)

	require.ErrorIs(t, svc.Data.DeleteAll(ctx, alice, "wrong password"), errors.ErrUnauthorized)
	require.NoError(t, svc.Data.DeleteAll(ctx, alice, "password1234"))

	accounts, err := svc.Accounts.List(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, accounts)
	cats, err := svc.Categories.Tree(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, cats)
	ops, _, err := svc.Operations.List(ctx, alice, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	assert.Empty(t, ops)
	plans, err := svc.Plans.List(ctx, alice, month(t, "2026-10"))
	require.NoError(t, err)
	assert.Empty(t, plans)
	imports, err := imp.List(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, imports)

	accounts, err = svc.Accounts.List(ctx, bob)
	require.NoError(t, err)
	assert.Len(t, accounts, 2, "other users keep their data")
	ops, _, err = svc.Operations.List(ctx, bob, domain.Filter{}, pagination.Params{})
	require.NoError(t, err)
	assert.Len(t, ops, 4, "2 split parts, a transfer and an import")
	imports, err = imp.List(ctx, bob)
	require.NoError(t, err)
	assert.Len(t, imports, 1)

	_, err = svc.Accounts.Create(ctx, alice, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err, "names are free again")
}

func TestAccountKinds(t *testing.T) {
	t.Parallel()

	d := repotest.New(t)
	svc := newServices(t, d)
	ctx := t.Context()
	uid := newUser(t, d)

	debit, err := svc.Accounts.Create(ctx, uid, domain.AccountInput{Name: "Debit", Currency: "RUB"})
	require.NoError(t, err)
	assert.Equal(t, domain.AccountDebit, debit.Kind, "debit by default")
	assert.False(t, debit.Kind.Liability())

	card, err := svc.Accounts.Create(ctx, uid, domain.AccountInput{
		Name: "Credit", Currency: "RUB", Kind: domain.AccountCredit, CreditLimit: dec("50000"),
	})
	require.NoError(t, err)
	assert.True(t, card.Kind.Liability())
	_, err = svc.Accounts.SetBalance(ctx, uid, card.ID, dec("-1200.50"), day(t, "2026-10-01"))
	require.NoError(t, err)
	card, err = svc.Accounts.Get(ctx, uid, card.ID)
	require.NoError(t, err)
	assert.Equal(t, "1200.5", card.Owed().String())
	assert.Equal(t, "48799.5", card.Available().String())
	assert.Equal(t, "50000", card.CreditLimit.String())

	tests := []struct {
		name string
		give domain.AccountInput
	}{
		{name: "unknown kind", give: domain.AccountInput{Name: "X", Currency: "RUB", Kind: "debt"}},
		{
			name: "negative limit",
			give: domain.AccountInput{Name: "X", Currency: "RUB", Kind: domain.AccountCredit, CreditLimit: dec("-1")},
		},
		{
			name: "limit precision",
			give: domain.AccountInput{
				Name:        "X",
				Currency:    "RUB",
				Kind:        domain.AccountCredit,
				CreditLimit: dec("1.001"),
			},
		},
		{name: "empty name", give: domain.AccountInput{Currency: "RUB", Kind: domain.AccountCredit}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := svc.Accounts.Create(t.Context(), uid, tt.give)
			require.ErrorIs(t, err, errors.ErrInvalid)
		})
	}

	err = svc.Accounts.Update(ctx, uid, card.ID, domain.AccountInput{Name: "Old card", Kind: domain.AccountDebit})
	require.NoError(t, err)
	card, err = svc.Accounts.Get(ctx, uid, card.ID)
	require.NoError(t, err)
	assert.Equal(t, "Old card", card.Name)
	assert.Equal(t, domain.AccountDebit, card.Kind)
	assert.True(t, card.CreditLimit.IsZero(), "limit cleared")
	assert.Equal(t, "RUB", card.Currency)

	err = svc.Accounts.Update(ctx, uid, card.ID, domain.AccountInput{Name: "Debit", Kind: domain.AccountDebit})
	require.ErrorIs(t, err, errors.ErrExists)
	err = svc.Accounts.Update(ctx, uid, 999, domain.AccountInput{Name: "Y"})
	require.ErrorIs(t, err, errors.ErrNotFound)
}

func TestBadImportPattern(t *testing.T) {
	t.Parallel()

	_, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
		Import:  service.ImportOptions{LoanPattern: `(`},
	})
	require.ErrorContains(t, err, "loan account pattern")
}

func TestDataBackup(t *testing.T) {
	t.Parallel()

	d := repotest.New(t)
	svc := newServices(t, d)
	ctx := t.Context()
	alice, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "admin")
	require.NoError(t, err)
	bob, err := svc.Auth.CreateUser(ctx, "bob", "password1234", "UTC", "user")
	require.NoError(t, err)
	_, err = svc.Accounts.Create(ctx, bob.ID, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	const passphrase = "correct horse battery"

	dir := t.TempDir()
	_, err = svc.Data.Backup(ctx, bob, "password1234", passphrase, dir)
	require.ErrorIs(t, err, errors.ErrForbidden)
	_, err = svc.Data.Backup(ctx, alice, "password1234", "short", dir)
	require.ErrorIs(t, err, errors.ErrInvalid)
	_, err = svc.Data.Backup(ctx, alice, "wrong password", passphrase, dir)
	require.ErrorIs(t, err, errors.ErrUnauthorized)

	enc, err := svc.Data.Backup(ctx, alice, "password1234", passphrase, dir)
	require.NoError(t, err)
	defer enc.Close()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the encrypted file is left")

	id, err := age.NewScryptIdentity(passphrase)
	require.NoError(t, err)
	plain, err := age.Decrypt(enc, id)
	require.NoError(t, err)
	restored := filepath.Join(t.TempDir(), "restored.db")
	out, err := os.Create(restored)
	require.NoError(t, err)
	_, err = io.Copy(out, plain)
	require.NoError(t, err)
	require.NoError(t, out.Close())

	store, err := repository.Open(ctx, restored)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	accounts, err := store.Read().Accounts.List(ctx, bob.ID)
	require.NoError(t, err)
	require.Len(t, accounts, 1, "the backup holds every user's data")
	assert.Equal(t, "Card", accounts[0].Name)
}
