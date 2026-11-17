package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/tbank"
)

// clock is a clock tests move.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fakeTBank is a T-Bank with made-up accounts and operations. It rotates the refresh
// token on every refresh.
type fakeTBank struct {
	mu       sync.Mutex
	accounts []tbank.Account
	ops      []tbank.Operation
	expired  bool
	// refreshed are the refresh tokens refreshed, froms the starts of operation requests
	refreshed []string
	froms     []time.Time
}

func (f *fakeTBank) StartLogin(context.Context, string, string) (*tbank.Login, error) {
	return &tbank.Login{DeviceID: "device", Step: tbank.StepOTP}, nil
}

func (f *fakeTBank) Confirm(context.Context, *tbank.Login, string) (tbank.Session, bool, error) {
	return tbank.Session{DeviceID: "device", AccessToken: "a0", RefreshToken: "r0"}, true, nil
}

func (f *fakeTBank) Refresh(_ context.Context, s tbank.Session) (tbank.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.expired {
		return tbank.Session{}, tbank.ErrSessionExpired
	}
	f.refreshed = append(f.refreshed, s.RefreshToken)
	s.RefreshToken = "r" + strconv.Itoa(len(f.refreshed))
	return s, nil
}

func (f *fakeTBank) Accounts(context.Context, tbank.Session) ([]tbank.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accounts, nil
}

func (f *fakeTBank) Operations(_ context.Context, _ tbank.Session, from, to time.Time) ([]tbank.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.froms = append(f.froms, from.UTC())
	var ops []tbank.Operation
	for _, op := range f.ops {
		if t := op.Time(); !t.Before(from) && !t.After(to) {
			ops = append(ops, op)
		}
	}
	return ops, nil
}

func (f *fakeTBank) addOps(t *testing.T, ops ...string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range ops {
		var op tbank.Operation
		require.NoError(t, json.Unmarshal([]byte(s), &op))
		f.ops = append(f.ops, op)
	}
}

// tbankOp is a made-up operation of account at time at; debited unless a hold.
func tbankOp(account, typ string, value int, at time.Time, hold bool) string {
	ms := at.UnixMilli()
	debited := fmt.Sprintf(`, "debitingTime": {"milliseconds": %d}`, ms)
	source := "PrimeTrxn"
	if hold {
		debited, source = "", "PrimeAuth"
	}
	return fmt.Sprintf(`{"id": "%d", "account": %q, "type": %q, "status": "OK", "description": "Магазин",
		"operationId": {"source": %q}, "operationTime": {"milliseconds": %d}%s,
		"accountAmount": {"value": %d}, "spendingCategory": {"name": "Супермаркеты"}}`,
		ms, account, typ, source, ms, debited, value)
}

// _uncategorized is the name of the default category of synced operations.
const _uncategorized = "Без категории"

type tbankFixture struct {
	store  *repository.Store
	svc    *service.Services
	bank   *fakeTBank
	clock  *clock
	userID int64
	black  domain.Account
}

// newTBankFixture is a user signed in to a T-Bank with accounts Black and Savings in
// RUB and Dollars in USD, and an account Black with 1000 on 2026-09-30.
func newTBankFixture(t *testing.T) tbankFixture {
	t.Helper()
	store := repotest.New(t)
	bank := &fakeTBank{}
	require.NoError(t, json.Unmarshal([]byte(`[
		{"id": "1001", "name": "Black", "accountType": "Current", "currency": {"name": "RUB"},
		 "moneyAmount": {"value": 1500}},
		{"id": "1002", "name": "Savings", "accountType": "Saving", "currency": {"name": "RUB"},
		 "moneyAmount": {"value": 20000}},
		{"id": "1003", "name": "Dollars", "accountType": "Current", "currency": {"name": "USD"},
		 "moneyAmount": {"value": 10}},
		{"id": "1004", "name": "Platinum", "accountType": "Credit", "currency": {"name": "RUB"},
		 "moneyAmount": {"value": 98000}, "creditLimit": {"value": 100000}}
	]`), &bank.accounts))
	c := &clock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	svc, err := service.New(store, c, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
		TBank: service.TBankOptions{
			Client: bank, Key: make([]byte, 32), SyncInterval: 15 * time.Minute, SyncOverlap: 14 * 24 * time.Hour,
		},
	})
	require.NoError(t, err)
	f := tbankFixture{store: store, svc: svc, bank: bank, clock: c, userID: newUser(t, store)}
	ctx := t.Context()

	step, err := svc.TBank.StartLogin(ctx, f.userID, "8 999 123-45-67")
	require.NoError(t, err)
	assert.Equal(t, tbank.StepOTP, step)
	step, err = svc.TBank.ConfirmLogin(ctx, f.userID, "1234")
	require.NoError(t, err)
	assert.Empty(t, step, "signed in")

	f.black, err = svc.Accounts.CreateWithBalance(ctx, f.userID,
		domain.AccountInput{Name: "My black", Currency: "RUB"}, dec("1000"), day(t, "2026-09-30"))
	require.NoError(t, err)
	return f
}

func (f tbankFixture) balance(t *testing.T) string {
	t.Helper()
	a, err := f.svc.Accounts.Get(t.Context(), f.userID, f.black.ID)
	require.NoError(t, err)
	return a.Balance.String()
}

// tbankAccount returns the stored T-Bank account of id at the bank.
func (f tbankFixture) tbankAccount(t *testing.T, id string) domain.BankAccount {
	t.Helper()
	accounts, err := f.svc.Banks.Accounts(t.Context(), f.userID)
	require.NoError(t, err)
	for _, a := range accounts {
		if a.Bank == domain.BankTBank && a.ExternalID == id {
			return a
		}
	}
	require.Failf(t, "no T-Bank account", "%s", id)
	return domain.BankAccount{}
}

// ops returns the operations of account id, newest first.
func (f tbankFixture) ops(t *testing.T, id int64) []domain.Operation {
	t.Helper()
	ops, _, err := f.svc.Operations.List(t.Context(), f.userID, domain.Filter{AccountID: id}, pagination.Params{})
	require.NoError(t, err)
	return ops
}

// category returns the category of id.
func (f tbankFixture) category(t *testing.T, id int64) domain.Category {
	t.Helper()
	c, err := f.store.Read().Categories.Get(t.Context(), f.userID, id)
	require.NoError(t, err)
	return c
}

func TestTBankLinkAndSync(t *testing.T) {
	t.Parallel()

	f := newTBankFixture(t)
	ctx := t.Context()
	accounts, err := f.svc.Banks.Accounts(ctx, f.userID)
	require.NoError(t, err)
	require.Len(t, accounts, 4, "listed at sign-in")
	assert.Equal(t, domain.AccountSavings, f.tbankAccount(t, "1002").Kind)
	platinum := f.tbankAccount(t, "1004")
	assert.Equal(t, domain.AccountCredit, platinum.Kind)
	assert.Equal(t, "-2000", platinum.Balance.String())
	assert.Equal(t, "100000", platinum.CreditLimit.String())

	groceries, err := f.svc.Categories.Create(ctx, f.userID, 0, domain.KindExpense, "Groceries")
	require.NoError(t, err)
	require.NoError(t, f.store.Write().Imports.MapCategory(ctx, f.userID, domain.ImportSourceTBank, "Супермаркеты",
		domain.KindExpense, groceries.ID))

	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.bank.addOps(t,
		tbankOp("1001", "Debit", 300, oct1.Add(-time.Hour), false), // before the chosen date
		tbankOp("1001", "Debit", 100, oct1.Add(10*time.Hour), false),
		tbankOp("1002", "Credit", 500, oct1.Add(11*time.Hour), false), // Savings is not linked
		tbankOp("1001", "Debit", 50, f.clock.Now().Add(-time.Hour), true),
	)

	_, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1003").ID, From: oct1}}, time.UTC,
		_uncategorized)
	require.ErrorIs(t, err, errors.ErrInvalid, "currencies differ")
	_, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: 0, From: oct1}}, time.UTC, _uncategorized)
	require.ErrorIs(t, err, errors.ErrInvalid)

	res, err := f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct1}}, time.UTC,
		_uncategorized)
	require.NoError(t, err)
	assert.Equal(t, domain.BankSync{Operations: 1, Fetched: map[int64]money.Amount{f.black.ID: dec("-100")}}, res,
		"the hold is imported once debited")
	assert.Equal(t, []time.Time{oct1}, f.bank.froms, "from the chosen date")
	assert.Equal(t, "900", f.balance(t), "the opening balance is not changed")
	ops := f.ops(t, f.black.ID)
	require.Len(t, ops, 2, "the opening balance and the operation")
	assert.Equal(t, _uncategorized, f.category(t, ops[0].CategoryID).Name, "the saved mapping is ignored")
	assert.NotEqual(t, groceries.ID, ops[0].CategoryID)
	black := f.tbankAccount(t, "1001")
	assert.Equal(t, f.black.ID, black.AccountID)
	assert.Equal(t, "1550", black.Balance.String(), "the bank balance less the hold")
	a, err := f.svc.Accounts.Get(ctx, f.userID, f.black.ID)
	require.NoError(t, err)
	assert.True(t, black.Mismatch(a))

	// a link is for good, one to one
	_, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: black.ID, From: oct1}}, time.UTC, _uncategorized)
	require.ErrorIs(t, err, errors.ErrConflict, "linked already")
	_, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1002").ID, From: oct1}}, time.UTC,
		_uncategorized)
	require.ErrorIs(t, err, errors.ErrConflict, "the account syncs with Black")
	other, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Other", Currency: "RUB"})
	require.NoError(t, err)
	_, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: other.ID, BankAccountID: black.ID, From: oct1}}, time.UTC, _uncategorized)
	require.ErrorIs(t, err, errors.ErrConflict, "Black syncs with another account")

	res, err = f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.True(t, res.Skipped, "synced recently")

	// the app is opened an hour later: only the period since the last sync is fetched
	synced := f.clock.Now()
	f.clock.add(time.Hour)
	f.bank.addOps(t, tbankOp("1001", "Debit", 200, f.clock.Now().Add(-time.Minute), false))
	res, err = f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Operations, "the synced operations are not imported again")
	require.Len(t, f.bank.froms, 2)
	assert.Equal(t, synced.Add(-14*24*time.Hour), f.bank.froms[1])
	assert.Equal(t, "700", f.balance(t))
	assert.Equal(t, []string{"r0", "r1"}, f.bank.refreshed, "the rotated refresh token is stored")

	// the bank no longer accepts the session
	f.bank.expired = true
	f.clock.add(time.Hour)
	res, err = f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.True(t, res.NeedsLogin)
	conn, err := f.svc.TBank.Connection(ctx, f.userID)
	require.NoError(t, err)
	assert.Equal(t, domain.BankLoginRequired, conn.Status)

	// deleting the account frees its bank account
	res, err = f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: other.ID, BankAccountID: platinum.ID, From: oct1}}, time.UTC, _uncategorized)
	require.NoError(t, err)
	assert.True(t, res.NeedsLogin, "linked, synced once signed in again")
	assert.Equal(t, other.ID, f.tbankAccount(t, "1004").AccountID)
	require.NoError(t, f.svc.Accounts.Delete(ctx, f.userID, other.ID))
	assert.Zero(t, f.tbankAccount(t, "1004").AccountID)
}

func TestTBankSyncReadsAccounts(t *testing.T) {
	t.Parallel()

	f := newTBankFixture(t)
	ctx := t.Context()
	f.bank.mu.Lock()
	f.bank.accounts[0].Name = "Black renamed"
	var card tbank.Account
	require.NoError(t, json.Unmarshal([]byte(`{"id": "1005", "name": "New card", "accountType": "Current",
		"currency": {"name": "RUB"}}`), &card))
	f.bank.accounts = append(f.bank.accounts, card)
	f.bank.mu.Unlock()

	res, err := f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.Equal(t, domain.BankSync{}, res, "nothing linked, nothing imported")
	assert.Empty(t, f.bank.froms, "no operations fetched")
	assert.Equal(t, "Black renamed", f.tbankAccount(t, "1001").Name, "read when the app is opened")
	assert.Equal(t, domain.AccountDebit, f.tbankAccount(t, "1005").Kind)
	accounts, err := f.svc.Banks.Accounts(ctx, f.userID)
	require.NoError(t, err)
	assert.Len(t, accounts, 5)

	res, err = f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.True(t, res.Skipped, "synced recently")
}

func TestTBankSyncUncategorizedAndRules(t *testing.T) {
	t.Parallel()

	f := newTBankFixture(t)
	ctx := t.Context()
	cafe, err := f.svc.Categories.Create(ctx, f.userID, 0, domain.KindExpense, "Cafe")
	require.NoError(t, err)
	_, err = f.svc.Rules.Create(ctx, f.userID, domain.RuleInput{
		Stage:      domain.RuleStageDefault,
		Match:      domain.RuleMatchAll,
		Conditions: []domain.Condition{{Field: domain.RuleFieldPayee, Op: domain.CondContains, Values: []string{"coffee"}}},
		Actions:    []domain.Action{{Type: domain.ActionSetCategory, Value: strconv.FormatInt(cafe.ID, 10)}},
	})
	require.NoError(t, err)

	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.bank.addOps(t,
		tbankOp("1001", "Debit", 100, oct1.Add(10*time.Hour), false),
		strings.Replace(tbankOp("1001", "Debit", 200, oct1.Add(11*time.Hour), false), "Магазин", "Coffee Like", 1),
		tbankOp("1001", "Credit", 50, oct1.Add(12*time.Hour), false),
	)

	// nothing is mapped, every operation is imported at once
	res, err := f.svc.TBank.Link(ctx, f.userID,
		[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct1}}, time.UTC,
		_uncategorized)
	require.NoError(t, err)
	assert.Equal(t, 3, res.Operations)
	ops := f.ops(t, f.black.ID)
	require.Len(t, ops, 4, "the opening balance and the operations")
	income, coffee, shop := ops[0], ops[1], ops[2]
	assert.Equal(t, cafe.ID, coffee.CategoryID, "rules categorize")
	assert.Equal(t, _uncategorized, f.category(t, shop.CategoryID).Name)
	assert.Equal(t, domain.KindExpense, f.category(t, shop.CategoryID).Kind)
	assert.Equal(t, _uncategorized, f.category(t, income.CategoryID).Name)
	assert.Equal(t, domain.KindIncome, f.category(t, income.CategoryID).Kind)

	// the next sync goes to the same categories
	f.clock.add(time.Hour)
	f.bank.addOps(t, tbankOp("1001", "Debit", 70, f.clock.Now().Add(-time.Minute), false))
	res, err = f.svc.TBank.Sync(ctx, f.userID, time.UTC, _uncategorized, false)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Operations)
	assert.Equal(t, shop.CategoryID, f.ops(t, f.black.ID)[0].CategoryID)
}

func TestTBankTransferWithUnlinked(t *testing.T) {
	t.Parallel()

	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := oct1.Add(10 * time.Hour)
	addOps := func(f tbankFixture) {
		f.bank.addOps(t,
			tbankOp("1001", "Debit", 300, at, false),
			tbankOp("1002", "Credit", 300, at.Add(20*time.Second), false), // Savings is not linked
			tbankOp("1002", "Credit", 900, at.Add(time.Hour), false),
		)
	}

	t.Run("mapped", func(t *testing.T) {
		t.Parallel()
		f := newTBankFixture(t)
		ctx := t.Context()
		addOps(f)
		savings, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Savings", Currency: "RUB"})
		require.NoError(t, err)
		require.NoError(t, f.store.Write().Imports.MapAccount(ctx, f.userID, domain.ImportSourceTBank, "Savings", "RUB",
			savings.ID))
		assert.Equal(t, savings.ID, f.tbankAccount(t, "1002").TransfersTo)

		res, err := f.svc.TBank.Link(ctx, f.userID,
			[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct1}}, time.UTC,
			_uncategorized)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Operations, "one transfer, the income of Savings is not imported")
		assert.Equal(t, "-300", res.Fetched[f.black.ID].String(), "the Savings side is not of the linked account")
		assert.Equal(t, domain.OpTransfer, f.ops(t, f.black.ID)[0].Type)
		assert.Equal(t, "700", f.balance(t))
		a, err := f.svc.Accounts.Get(ctx, f.userID, savings.ID)
		require.NoError(t, err)
		assert.Equal(t, "300", a.Balance.String())
		assert.Zero(t, f.tbankAccount(t, "1001").TransfersTo, "linked")
	})

	t.Run("not mapped", func(t *testing.T) {
		t.Parallel()
		f := newTBankFixture(t)
		addOps(f)
		res, err := f.svc.TBank.Link(t.Context(), f.userID,
			[]domain.BankLink{{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct1}}, time.UTC,
			_uncategorized)
		require.NoError(t, err)
		assert.Equal(t, 1, res.Operations, "the Black side only")
		op := f.ops(t, f.black.ID)[0]
		assert.Equal(t, domain.OpExpense, op.Type)
		assert.Equal(t, _uncategorized, f.category(t, op.CategoryID).Name)
		assert.Equal(t, "700", f.balance(t))
	})
}

func TestTBankLinkSeveral(t *testing.T) {
	t.Parallel()
	f := newTBankFixture(t)
	ctx := t.Context()
	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := oct1.Add(10 * time.Hour)
	f.bank.addOps(t,
		tbankOp("1001", "Debit", 300, at, false),
		tbankOp("1002", "Credit", 300, at.Add(20*time.Second), false),
		tbankOp("1002", "Credit", 900, at.Add(time.Hour), false),
	)
	savings, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Savings", Currency: "RUB"})
	require.NoError(t, err)

	res, err := f.svc.TBank.Link(ctx, f.userID, []domain.BankLink{
		{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct1},
		{AccountID: savings.ID, BankAccountID: f.tbankAccount(t, "1002").ID, From: oct1},
	}, time.UTC, _uncategorized)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Operations, "the transfer and the income of Savings")
	assert.Equal(t, map[int64]money.Amount{f.black.ID: dec("-300"), savings.ID: dec("1200")}, res.Fetched)
	assert.Equal(t, domain.OpTransfer, f.ops(t, f.black.ID)[0].Type)
	assert.Equal(t, savings.ID, f.tbankAccount(t, "1002").AccountID)
}

func TestTBankLinkFrom(t *testing.T) {
	t.Parallel()
	f := newTBankFixture(t)
	ctx := t.Context()
	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	oct2 := oct1.AddDate(0, 0, 1)
	at := oct1.Add(10 * time.Hour)
	f.bank.addOps(t,
		tbankOp("1001", "Debit", 300, at, false), // imported before, by a CSV export
		tbankOp("1002", "Credit", 300, at.Add(20*time.Second), false),
		tbankOp("1001", "Debit", 50, at.AddDate(0, 0, 1), false),
	)
	savings, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Savings", Currency: "RUB"})
	require.NoError(t, err)

	res, err := f.svc.TBank.Link(ctx, f.userID, []domain.BankLink{
		{AccountID: f.black.ID, BankAccountID: f.tbankAccount(t, "1001").ID, From: oct2},
		{AccountID: savings.ID, BankAccountID: f.tbankAccount(t, "1002").ID, From: oct1},
	}, time.UTC, _uncategorized)
	require.NoError(t, err)
	assert.Equal(t, map[int64]money.Amount{f.black.ID: dec("-50"), savings.ID: dec("300")}, res.Fetched,
		"Black since its start, Savings since its own")
	ops := f.ops(t, f.black.ID)
	assert.Equal(t, domain.OpExpense, ops[0].Type)
	assert.Equal(t, "50", ops[0].Amount.String())
}
