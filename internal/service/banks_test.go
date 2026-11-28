package service_test

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
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

type fakeLogin struct{ step string }

func (l *fakeLogin) Step() string { return l.step }

// fakeBank is a bank with made-up accounts and operations. Its sessions are s0, s1,
// ..., one more on every refresh.
type fakeBank struct {
	mu       sync.Mutex
	accounts []domain.BankAccount
	records  map[string][]domain.Record
	expired  bool
	// fetchErr fails the fetches while set
	fetchErr error
	// refreshed are the sessions refreshed, froms the starts of the fetches
	refreshed []string
	froms     []time.Time
}

//nolint:ireturn // implements service.BankClient
func (f *fakeBank) StartLogin(context.Context, []byte, string) (domain.BankLogin, error) {
	return &fakeLogin{step: domain.BankStepOTP}, nil
}

func (f *fakeBank) Confirm(context.Context, domain.BankLogin, string) ([]byte, bool, error) {
	return []byte("s0"), true, nil
}

func (f *fakeBank) Refresh(_ context.Context, s []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.expired {
		return nil, domain.ErrBankLoginRequired
	}
	f.refreshed = append(f.refreshed, string(s))
	return []byte("s" + strconv.Itoa(len(f.refreshed))), nil
}

func (f *fakeBank) Fetch(_ context.Context, _ []byte, from, to time.Time, _ *time.Location) (domain.BankData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.froms = append(f.froms, from.UTC())
	if f.fetchErr != nil {
		return domain.BankData{}, f.fetchErr
	}
	data := domain.BankData{Accounts: f.accounts, Records: make(map[string][]domain.Record)}
	for id, rs := range f.records {
		for _, r := range rs {
			if !r.Time.Before(from) && !r.Time.After(to) {
				data.Records[id] = append(data.Records[id], r)
			}
		}
	}
	return data, nil
}

// add adds an operation of the account id at the bank.
func (f *fakeBank) add(id, amount string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.accounts {
		if a.ExternalID == id {
			f.records[id] = append(f.records[id], domain.Record{
				Account: a.Name, Time: at, Amount: dec(amount), Currency: a.Currency, Category: "Супермаркеты",
				Payee: "Магазин", ExternalID: id + "-" + strconv.Itoa(len(f.records[id])),
			})
		}
	}
}

type bankFixture struct {
	store  *repository.Store
	svc    *service.Services
	bank   *fakeBank
	clock  *clock
	userID int64
	black  domain.Account
}

// newBankFixture is a user signed in to a T-Bank with accounts Black, Savings and Zero
// in RUB and Dollars in USD, and an account Black with 1000 on 2026-09-30.
func newBankFixture(t *testing.T) bankFixture {
	t.Helper()
	store := repotest.New(t)
	bank := &fakeBank{
		accounts: []domain.BankAccount{
			{ExternalID: "1001", Name: "Black", Currency: "RUB", Kind: domain.AccountDebit, Balance: dec("1500")},
			{ExternalID: "1002", Name: "Savings", Currency: "RUB", Kind: domain.AccountSavings, Balance: dec("20000")},
			{ExternalID: "1003", Name: "Dollars", Currency: "USD", Kind: domain.AccountDebit, Balance: dec("10")},
			{ExternalID: "1004", Name: "Zero", Currency: "RUB", Kind: domain.AccountDebit},
		},
		records: make(map[string][]domain.Record),
	}
	c := &clock{now: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}
	svc, err := service.New(store, c, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
		Banks: service.BankOptions{
			Clients: map[domain.Bank]service.BankClient{domain.BankTBank: bank},
			Key:     make([]byte, 32), SyncInterval: 15 * time.Minute, SyncOverlap: 14 * 24 * time.Hour,
		},
	})
	require.NoError(t, err)
	f := bankFixture{store: store, svc: svc, bank: bank, clock: c, userID: newUser(t, store)}
	ctx := t.Context()

	step, err := svc.Banks.StartLogin(ctx, f.userID, domain.BankTBank, "+79991234567")
	require.NoError(t, err)
	assert.Equal(t, domain.BankStepOTP, step)
	step, err = svc.Banks.ConfirmLogin(ctx, f.userID, domain.BankTBank, "1234")
	require.NoError(t, err)
	assert.Empty(t, step, "signed in")
	_, err = svc.Banks.Sync(ctx, f.userID, domain.BankTBank, time.UTC, true)
	require.NoError(t, err, "the accounts are read")

	f.black, err = svc.Accounts.CreateWithBalance(ctx, f.userID,
		domain.AccountInput{Name: "My black", Currency: "RUB"}, dec("1000"), day(t, "2026-09-30"))
	require.NoError(t, err)
	return f
}

func (f bankFixture) balance(t *testing.T, id int64) string {
	t.Helper()
	a, err := f.svc.Accounts.Get(t.Context(), f.userID, id)
	require.NoError(t, err)
	return a.Balance.String()
}

// bankAccount returns the stored bank account of id at the bank.
func (f bankFixture) bankAccount(t *testing.T, id string) domain.BankAccount {
	t.Helper()
	accounts, err := f.svc.Banks.Accounts(t.Context(), f.userID)
	require.NoError(t, err)
	for _, a := range accounts {
		if a.Bank == domain.BankTBank && a.ExternalID == id {
			return a
		}
	}
	require.Failf(t, "no bank account", "%s", id)
	return domain.BankAccount{}
}

// link links the accounts to the bank accounts of ids at the bank, in pairs.
func (f bankFixture) link(t *testing.T, pairs ...any) (domain.BankSync, error) {
	t.Helper()
	var links []domain.BankLink
	for i := 0; i < len(pairs); i += 2 {
		accountID, ok := pairs[i].(int64)
		require.True(t, ok)
		id, ok := pairs[i+1].(string)
		require.True(t, ok)
		links = append(links, domain.BankLink{AccountID: accountID, BankAccountID: f.bankAccount(t, id).ID})
	}
	return f.svc.Banks.Link(t.Context(), f.userID, domain.BankTBank, links, time.UTC)
}

func (f bankFixture) sync(t *testing.T, force bool) domain.BankSync {
	t.Helper()
	res, err := f.svc.Banks.Sync(t.Context(), f.userID, domain.BankTBank, time.UTC, force)
	require.NoError(t, err)
	return res
}

// ops returns the operations of account id, newest first.
func (f bankFixture) ops(t *testing.T, id int64) []domain.Operation {
	t.Helper()
	ops, _, err := f.svc.Operations.List(t.Context(), f.userID, domain.Filter{AccountID: id},
		pagination.NewParams(pagination.Cursor{}, 0))
	require.NoError(t, err)
	return ops
}

func TestBanksLinkAndSync(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	ctx := t.Context()
	accounts, err := f.svc.Banks.Accounts(ctx, f.userID)
	require.NoError(t, err)
	require.Len(t, accounts, 4, "read once signed in")

	groceries, err := f.svc.Categories.Create(ctx, f.userID, 0, domain.KindExpense, "Groceries")
	require.NoError(t, err)
	require.NoError(t, f.store.Write().Imports.MapCategory(ctx, f.userID, domain.ImportSourceTBank, "Супермаркеты",
		domain.KindExpense, groceries.ID))

	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.bank.add("1001", "-300", oct1.Add(-time.Hour)) // before the day after the last operation
	f.bank.add("1001", "-100", oct1.Add(10*time.Hour))
	f.bank.add("1002", "500", oct1.Add(11*time.Hour)) // Savings is not linked

	_, err = f.link(t, f.black.ID, "1003")
	require.ErrorIs(t, err, errors.ErrInvalid, "currencies differ")
	_, err = f.svc.Banks.Link(ctx, f.userID, domain.BankTBank, []domain.BankLink{{AccountID: f.black.ID}}, time.UTC)
	require.ErrorIs(t, err, errors.ErrInvalid)

	res, err := f.link(t, f.black.ID, "1001")
	require.NoError(t, err)
	assert.Equal(t, domain.BankSync{Operations: 1}, res)
	assert.Equal(t, oct1, f.bank.froms[len(f.bank.froms)-1], "from the day after the last operation")
	assert.Equal(t, "900", f.balance(t, f.black.ID), "the opening balance is not changed")
	ops := f.ops(t, f.black.ID)
	require.Len(t, ops, 2, "the opening balance and the operation")
	assert.Zero(t, ops[0].CategoryID, "the saved mapping is ignored")
	black := f.bankAccount(t, "1001")
	assert.Equal(t, f.black.ID, black.AccountID)
	a, err := f.svc.Accounts.Get(ctx, f.userID, f.black.ID)
	require.NoError(t, err)
	assert.True(t, black.IsMismatched(a))

	// a link is for good, one to one
	_, err = f.link(t, f.black.ID, "1001")
	require.ErrorIs(t, err, errors.ErrConflict, "linked already")
	_, err = f.link(t, f.black.ID, "1002")
	require.ErrorIs(t, err, errors.ErrConflict, "the account syncs with Black")
	other, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Other", Currency: "RUB"})
	require.NoError(t, err)
	_, err = f.link(t, other.ID, "1001")
	require.ErrorIs(t, err, errors.ErrConflict, "Black syncs with another account")

	fetched := len(f.bank.froms)
	assert.Equal(t, domain.BankSync{}, f.sync(t, false), "synced recently")
	assert.Len(t, f.bank.froms, fetched)
	res, err = f.svc.Banks.SyncAll(ctx, f.userID, time.UTC, false)
	require.NoError(t, err)
	assert.Equal(t, domain.BankSync{}, res)

	// the app is opened an hour later: only the period since the last sync is fetched
	synced := f.clock.Now()
	f.clock.add(time.Hour)
	f.bank.add("1001", "-200", f.clock.Now().Add(-time.Minute))
	res, err = f.svc.Banks.SyncAll(ctx, f.userID, time.UTC, false)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Operations, "the synced operations are not imported again")
	assert.Equal(t, synced.Add(-14*24*time.Hour), f.bank.froms[len(f.bank.froms)-1])
	assert.Equal(t, "700", f.balance(t, f.black.ID))
	assert.Equal(t, []string{"s0", "s1", "s2"}, f.bank.refreshed, "the refreshed session is stored")

	// a new account starts at the bank balance; deleting it frees its bank account
	res, err = f.link(t, other.ID, "1004")
	require.NoError(t, err)
	assert.Zero(t, res.Operations)
	assert.Equal(t, "0", f.balance(t, other.ID))
	assert.Equal(t, other.ID, f.bankAccount(t, "1004").AccountID)
	require.NoError(t, f.svc.Accounts.Delete(ctx, f.userID, other.ID))
	assert.Zero(t, f.bankAccount(t, "1004").AccountID)

	// the bank no longer accepts the session
	f.bank.expired = true
	f.clock.add(time.Hour)
	assert.Equal(t, []domain.Bank{domain.BankTBank}, f.sync(t, false).NeedsLogin)
	conn, err := f.svc.Banks.Connection(ctx, f.userID, domain.BankTBank)
	require.NoError(t, err)
	assert.Equal(t, domain.BankLoginRequired, conn.Status)
	res, err = f.link(t, other.ID, "1002")
	require.NoError(t, err)
	assert.Equal(t, []domain.Bank{domain.BankTBank}, res.NeedsLogin, "not linked until signed in again")
	assert.Zero(t, f.bankAccount(t, "1002").AccountID)
}

func TestBanksLinkFailedSync(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	f.bank.add("1002", "-500", time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC))
	f.bank.add("1002", "700", time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))

	f.bank.mu.Lock()
	f.bank.fetchErr = errors.Error("bank is down")
	f.bank.mu.Unlock()
	_, err := f.link(t, int64(0), "1002")
	require.Error(t, err)
	savings := f.bankAccount(t, "1002")
	require.NotZero(t, savings.AccountID, "linked though the sync failed")
	assert.Equal(t, "0", f.balance(t, savings.AccountID))

	f.bank.mu.Lock()
	f.bank.fetchErr = nil
	f.bank.mu.Unlock()
	f.clock.add(time.Hour)
	res := f.sync(t, false)
	assert.Equal(t, 2, res.Operations, "the whole history the link missed")
	assert.Equal(t, time.Unix(0, 0).UTC(), f.bank.froms[len(f.bank.froms)-1])
	assert.Equal(t, "20000", f.balance(t, savings.AccountID), "the opening balance is set")

	f.clock.add(time.Hour)
	f.bank.add("1002", "-100", f.clock.Now().Add(-time.Minute))
	assert.Equal(t, 1, f.sync(t, false).Operations)
	assert.Equal(t, "19900", f.balance(t, savings.AccountID), "the opening balance is set once")
}

func TestBanksSyncReadsAccounts(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	ctx := t.Context()
	f.bank.mu.Lock()
	f.bank.accounts[0].Name = "Black renamed"
	f.bank.accounts = append(f.bank.accounts,
		domain.BankAccount{ExternalID: "1005", Name: "New card", Currency: "RUB", Kind: domain.AccountDebit})
	f.bank.mu.Unlock()

	assert.Equal(t, domain.BankSync{}, f.sync(t, true), "nothing linked, nothing imported")
	assert.Equal(t, "Black renamed", f.bankAccount(t, "1001").Name)
	accounts, err := f.svc.Banks.Accounts(ctx, f.userID)
	require.NoError(t, err)
	assert.Len(t, accounts, 5)

	fetched := len(f.bank.froms)
	f.sync(t, false)
	assert.Len(t, f.bank.froms, fetched, "synced recently")
}

func TestBanksSyncNoCategoryAndRules(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
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
	f.bank.add("1001", "-100", oct1.Add(10*time.Hour))
	f.bank.add("1001", "-200", oct1.Add(11*time.Hour))
	f.bank.records["1001"][1].Payee = "Coffee Like"
	f.bank.add("1001", "50", oct1.Add(12*time.Hour))

	// nothing is mapped, every operation is imported at once
	res, err := f.link(t, f.black.ID, "1001")
	require.NoError(t, err)
	assert.Equal(t, 3, res.Operations)
	ops := f.ops(t, f.black.ID)
	require.Len(t, ops, 4, "the opening balance and the operations")
	income, coffee, shop := ops[0], ops[1], ops[2]
	assert.Equal(t, cafe.ID, coffee.CategoryID, "rules categorize")
	assert.Zero(t, shop.CategoryID, "no rule matches")
	assert.Zero(t, income.CategoryID, "no rule matches")
	tree, err := f.svc.Categories.Tree(ctx, f.userID)
	require.NoError(t, err)
	assert.Len(t, tree, 1, "no category is created")
}

func TestBanksSyncNewOnly(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	ctx := t.Context()
	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	f.bank.add("1001", "-100", oct1.Add(10*time.Hour))
	f.bank.add("1001", "-200", oct1.Add(11*time.Hour))
	res, err := f.link(t, f.black.ID, "1001")
	require.NoError(t, err)
	require.Equal(t, 2, res.Operations)

	// the bank renames the payee and moves the time of a synced operation: it is the
	// same operation, by its id
	f.bank.records["1001"][1].Payee = "Магазин у дома"
	f.bank.records["1001"][1].Time = oct1.Add(11*time.Hour + time.Minute)
	f.clock.add(time.Hour)
	assert.Zero(t, f.sync(t, true).Operations, "an operation the bank changed is not imported again")

	f.bank.add("1001", "-50", f.clock.Now().Add(-time.Minute))
	f.clock.add(30 * time.Minute)
	assert.Equal(t, 1, f.sync(t, true).Operations)
	assert.Len(t, f.ops(t, f.black.ID), 4, "the opening balance and the operations")

	imports, err := f.svc.Imports.List(ctx, f.userID)
	require.NoError(t, err)
	require.Len(t, imports, 2, "the link and the sync with a new operation")
	assert.Equal(t, int64(1), imports[0].RowsTotal, "only the new operation is staged")
	assert.Zero(t, imports[0].RowsDup)
	p, err := f.svc.Imports.Preview(ctx, f.userID, imports[0].ID)
	require.NoError(t, err)
	require.Len(t, p.Rows, 1)
	assert.Equal(t, "1001-2", p.Rows[0].Row.ExternalID)
}

func TestBanksSyncCreditLimit(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	ctx := t.Context()
	f.bank.accounts = append(f.bank.accounts, domain.BankAccount{
		ExternalID: "1005", Name: "Platinum", Currency: "RUB", Kind: domain.AccountCredit,
		Balance: dec("-20000"), CreditLimit: dec("100000"),
	})
	f.sync(t, true)

	// an account created before it is linked takes the kind and limit of the card
	_, err := f.link(t, f.black.ID, "1005")
	require.NoError(t, err)
	a, err := f.svc.Accounts.Get(ctx, f.userID, f.black.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountCredit, a.Kind)
	assert.Equal(t, "100000", a.CreditLimit.String())

	f.bank.accounts[len(f.bank.accounts)-1].CreditLimit = dec("150000")
	f.clock.add(time.Hour)
	f.sync(t, true)
	a, err = f.svc.Accounts.Get(ctx, f.userID, f.black.ID)
	require.NoError(t, err)
	assert.Equal(t, "150000", a.CreditLimit.String(), "a changed limit follows the bank")
}

func TestBanksTransferWithUnlinked(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	addOps := func(f bankFixture) {
		f.bank.add("1001", "-300", at)
		f.bank.add("1002", "300", at.Add(20*time.Second)) // Savings is not linked
		f.bank.add("1002", "900", at.Add(time.Hour))
	}

	t.Run("mapped", func(t *testing.T) {
		t.Parallel()
		f := newBankFixture(t)
		ctx := t.Context()
		addOps(f)
		savings, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Savings", Currency: "RUB"})
		require.NoError(t, err)
		require.NoError(t, f.store.Write().Imports.MapAccount(ctx, f.userID, domain.ImportSourceTBank, "Savings", "RUB",
			savings.ID))
		assert.Equal(t, savings.ID, f.bankAccount(t, "1002").TransfersTo)

		res, err := f.link(t, f.black.ID, "1001")
		require.NoError(t, err)
		assert.Equal(t, 1, res.Operations, "one transfer, the income of Savings is not imported")
		assert.Equal(t, domain.OpTransfer, f.ops(t, f.black.ID)[0].Type)
		assert.Equal(t, "700", f.balance(t, f.black.ID))
		assert.Equal(t, "300", f.balance(t, savings.ID))
		assert.Zero(t, f.bankAccount(t, "1001").TransfersTo, "linked")
	})

	t.Run("not mapped", func(t *testing.T) {
		t.Parallel()
		f := newBankFixture(t)
		addOps(f)
		res, err := f.link(t, f.black.ID, "1001")
		require.NoError(t, err)
		assert.Equal(t, 1, res.Operations, "the Black side only")
		op := f.ops(t, f.black.ID)[0]
		assert.Equal(t, domain.OpExpense, op.Type)
		assert.Zero(t, op.CategoryID)
		assert.Equal(t, "700", f.balance(t, f.black.ID))
	})
}

func TestBanksLinkSeveral(t *testing.T) {
	t.Parallel()
	f := newBankFixture(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	f.bank.add("1001", "-300", at)
	f.bank.add("1002", "300", at.Add(20*time.Second))
	f.bank.add("1002", "900", at.Add(time.Hour))
	savings, err := f.svc.Accounts.Create(ctx, f.userID, domain.AccountInput{Name: "Savings", Currency: "RUB"})
	require.NoError(t, err)

	res, err := f.link(t, f.black.ID, "1001", savings.ID, "1002")
	require.NoError(t, err)
	assert.Equal(t, 2, res.Operations, "the transfer and the income of Savings")
	assert.Equal(t, domain.OpTransfer, f.ops(t, f.black.ID)[0].Type)
	assert.Equal(t, savings.ID, f.bankAccount(t, "1002").AccountID)
	assert.Equal(t, "700", f.balance(t, f.black.ID))
	assert.Equal(t, "20000", f.balance(t, savings.ID), "without operations, it starts at the bank balance")
	ops := f.ops(t, savings.ID)
	require.Len(t, ops, 3)
	assert.Equal(t, day(t, "2026-09-30"), ops[2].Date, "the opening balance is before the first operation")
}

func TestBanksSyncNotBeforeLink(t *testing.T) {
	t.Parallel()

	f := newBankFixture(t)
	ctx := t.Context()
	// the account has operations up to Nov 10, within the overlap of a sync on Nov 15
	nov10 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	_, err := f.svc.Accounts.SetBalance(ctx, f.userID, f.black.ID, dec("800"), nov10)
	require.NoError(t, err)
	// in gigafin already, entered by hand, so its hash is not the bank's
	f.bank.add("1001", "-200", nov10.Add(-24*time.Hour))
	f.bank.add("1001", "-100", nov10.Add(36*time.Hour))

	res, err := f.link(t, f.black.ID, "1001")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Operations, "since the day after the last operation")
	assert.Equal(t, "700", f.balance(t, f.black.ID))

	f.clock.add(time.Hour)
	assert.Zero(t, f.sync(t, true).Operations, "the overlap does not go back past the link")
	assert.Equal(t, "700", f.balance(t, f.black.ID))
}

func TestBanksLinkStart(t *testing.T) {
	t.Parallel()
	f := newBankFixture(t)
	ctx := t.Context()
	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := oct1.Add(10 * time.Hour)
	// imported before, by a CSV export: the balance on Oct 1 is set
	_, err := f.svc.Accounts.SetBalance(ctx, f.userID, f.black.ID, dec("700"), oct1)
	require.NoError(t, err)
	f.bank.add("1001", "-300", at)
	f.bank.add("1002", "300", at.Add(20*time.Second))
	f.bank.add("1001", "-50", at.AddDate(0, 0, 1))

	res, err := f.svc.Banks.Link(ctx, f.userID, domain.BankTBank, []domain.BankLink{
		{AccountID: f.black.ID, BankAccountID: f.bankAccount(t, "1001").ID},
		{BankAccountID: f.bankAccount(t, "1002").ID},
	}, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Operations, "Black since the day after its last operation, Savings all")
	ops := f.ops(t, f.black.ID)
	assert.Equal(t, domain.OpExpense, ops[0].Type)
	assert.Equal(t, "50", ops[0].Amount.String())
	assert.Equal(t, "650", f.balance(t, f.black.ID))

	savings := f.bankAccount(t, "1002")
	a, err := f.svc.Accounts.Get(ctx, f.userID, savings.AccountID)
	require.NoError(t, err)
	assert.Equal(t, "Savings", a.Name, "created from the bank account")
	assert.Equal(t, domain.AccountSavings, a.Kind)
	assert.Equal(t, "20000", a.Balance.String())
}
