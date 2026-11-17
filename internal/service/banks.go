package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// _bankLoginTTL is how long a sign-in waits for the next step.
const _bankLoginTTL = 10 * time.Minute

// _bankSyncTimeout bounds a sync, which goes on when the request that started it ends.
const _bankSyncTimeout = 2 * time.Minute

// BankClient is the side of a bank in syncing: signing in and reading accounts and
// operations. Sessions are opaque to gigafin, which seals and stores them.
type BankClient interface {
	// StartLogin starts a sign-in by phone in the international form. prev is the
	// session of the last sign-in, nil without one.
	StartLogin(ctx context.Context, prev []byte, phone string) (domain.BankLogin, error)
	// Confirm answers the step l asks for with value. done is false when the bank asks
	// for another step, which l names; otherwise it returns the session signed in.
	Confirm(ctx context.Context, l domain.BankLogin, value string) (session []byte, done bool, err error)
	// Refresh returns a session in place of session, which may stop working. It fails
	// with domain.ErrBankLoginRequired when the bank no longer accepts session.
	Refresh(ctx context.Context, session []byte) ([]byte, error)
	// Fetch returns the accounts as of now and the operations between from and to,
	// with times in loc. It fails with domain.ErrBankLoginRequired as Refresh does.
	Fetch(ctx context.Context, session []byte, from, to time.Time, loc *time.Location) (domain.BankData, error)
}

// BankOptions tune bank sync. Without Key it is disabled.
type BankOptions struct {
	// Clients are the banks users may sync with.
	Clients map[domain.Bank]BankClient
	// Key encrypts bank sessions stored in the database, 32 bytes.
	Key []byte
	// SyncInterval is the least time between two syncs of a bank that are not forced.
	SyncInterval time.Duration
	// SyncOverlap is how far before the end of the last synced period a sync starts.
	SyncOverlap time.Duration
}

// Banks signs users in to banks, keeps their bank accounts and syncs the operations of
// the ones linked to accounts into imports, committed at once. Balances are never
// changed but for the opening balance of a new link; the bank balance is kept to
// compare.
type Banks struct {
	store    *repository.Store
	clock    timex.Clock
	log      *slog.Logger
	imports  *Imports
	accounts *Accounts
	aead     cipher.AEAD
	opts     BankOptions

	// mu guards pending and locks
	mu      sync.Mutex
	pending map[pendingBankKey]pendingBankLogin
	// locks has a lock per user, so syncs and sign-ins of a user do not overlap
	locks map[int64]*sync.Mutex
}

type pendingBankKey struct {
	userID int64
	bank   domain.Bank
}

type pendingBankLogin struct {
	login domain.BankLogin
	// step is the step of login as of its last answer, read without calling the bank
	step    string
	phone   string
	expires time.Time
}

// bankSum sums the records a sync imports into an account.
type bankSum struct {
	sum money.Amount
	// first is the time of the earliest record
	first time.Time
}

func newBanks(
	store *repository.Store, clock timex.Clock, log *slog.Logger, imports *Imports, accounts *Accounts, opts BankOptions,
) (*Banks, error) {
	s := &Banks{
		store: store, clock: clock, log: log, imports: imports, accounts: accounts, opts: opts,
		pending: make(map[pendingBankKey]pendingBankLogin), locks: make(map[int64]*sync.Mutex),
	}
	if len(opts.Key) == 0 {
		return s, nil
	}
	block, err := aes.NewCipher(opts.Key)
	if err != nil {
		return nil, fmt.Errorf("bank secret key: %w", err)
	}
	if s.aead, err = cipher.NewGCM(block); err != nil {
		return nil, fmt.Errorf("bank secret key: %w", err)
	}
	return s, nil
}

// Enabled returns the banks whose sync is configured.
func (s *Banks) Enabled() []domain.Bank {
	if s.aead == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(s.opts.Clients))
}

// client returns the client of bank, failing when its sync is disabled.
//
//nolint:ireturn // banks are known by their clients only
func (s *Banks) client(bank domain.Bank) (BankClient, error) {
	c, ok := s.opts.Clients[bank]
	if !ok || s.aead == nil {
		return nil, domain.NewError(errors.ErrConflict, "%s: sync is disabled", domain.Msg(bank.Name()))
	}
	return c, nil
}

// Connection returns the user's connection to bank, errors.ErrNotFound when there is
// none.
func (s *Banks) Connection(ctx context.Context, userID int64, bank domain.Bank) (domain.BankConnection, error) {
	return s.store.Read().Banks.Connection(ctx, userID, bank)
}

// Accounts returns the user's accounts at the enabled banks as of the last sign-in or
// sync, and the accounts the transfers with the ones not linked go to.
func (s *Banks) Accounts(ctx context.Context, userID int64) ([]domain.BankAccount, error) {
	var accounts []domain.BankAccount
	for _, bank := range s.Enabled() {
		ts, err := bankAccounts(ctx, s.store.Read(), userID, bank)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, ts...)
	}
	return accounts, nil
}

// bankAccounts returns the user's accounts at bank with the accounts the transfers
// with the ones not linked go to: those their names map to in imports of the bank.
func bankAccounts(
	ctx context.Context, r repository.Repos, userID int64, bank domain.Bank,
) ([]domain.BankAccount, error) {
	all, err := r.Banks.Accounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	m, err := r.Imports.Mapping(ctx, userID, bank.ImportSource())
	if err != nil {
		return nil, err
	}
	var accounts []domain.BankAccount
	for _, t := range all {
		if t.Bank != bank {
			continue
		}
		if a, ok := m.Accounts[domain.AccountKey(t.Name, t.Currency)]; ok && t.AccountID == 0 {
			t.TransfersTo = a.ID
		}
		accounts = append(accounts, t)
	}
	return accounts, nil
}

// LoginStep returns the input a sign-in of the user to bank in progress waits for and
// its phone, empty when there is none.
func (s *Banks) LoginStep(userID int64, bank domain.Bank) (step, phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[pendingBankKey{userID, bank}]
	if !ok || s.clock.Now().After(p.expires) {
		return "", ""
	}
	return p.step, p.phone
}

// StartLogin starts signing the user in to bank by phone in the international form.
// It returns the step the sign-in waits for, such as the SMS code.
func (s *Banks) StartLogin(ctx context.Context, userID int64, bank domain.Bank, phone string) (string, error) {
	c, err := s.client(bank)
	if err != nil {
		return "", err
	}
	// the session of the last sign-in is passed on, so the bank does not see a new device
	var prev []byte
	if conn, err := s.Connection(ctx, userID, bank); err == nil {
		if sess, err := s.open(userID, conn.Secret); err == nil {
			prev = sess
		}
	}
	l, err := c.StartLogin(ctx, prev, phone)
	if err != nil {
		return "", domain.NewError(errors.ErrInvalid, "the bank refused to sign in: %v", err)
	}
	s.mu.Lock()
	s.pending[pendingBankKey{userID, bank}] = pendingBankLogin{
		login: l, step: l.Step(), phone: phone, expires: s.clock.Now().Add(_bankLoginTTL),
	}
	s.mu.Unlock()
	s.log.InfoContext(ctx, "bank login started", slog.Int64("user_id", userID), slog.String("bank", string(bank)))
	return l.Step(), nil
}

// ConfirmLogin answers the step the sign-in to bank waits for, such as the SMS code or
// the password. It returns the next step, empty when the user is signed in.
func (s *Banks) ConfirmLogin(ctx context.Context, userID int64, bank domain.Bank, value string) (string, error) {
	c, err := s.client(bank)
	if err != nil {
		return "", err
	}
	// a sign-in is answered once at a time
	unlock := s.lock(userID)
	defer unlock()
	key := pendingBankKey{userID, bank}
	s.mu.Lock()
	p, ok := s.pending[key]
	s.mu.Unlock()
	if !ok || s.clock.Now().After(p.expires) {
		return "", domain.NewError(errors.ErrInvalid, "sign-in expired, start again")
	}
	sess, done, err := c.Confirm(ctx, p.login, value)
	p.step = p.login.Step()
	s.mu.Lock()
	s.pending[key] = p
	s.mu.Unlock()
	if err != nil {
		return p.step, domain.NewError(errors.ErrInvalid, "the bank did not accept it: %v", err)
	}
	if !done {
		return p.step, nil
	}
	secret, err := s.seal(userID, sess)
	if err != nil {
		return "", err
	}
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		conn, err := r.Banks.Connection(ctx, userID, bank)
		if err != nil && !errors.Is(err, errors.ErrNotFound) {
			return err
		}
		conn.Bank, conn.Login, conn.Status, conn.Secret, conn.LastError =
			bank, p.phone, domain.BankConnected, secret, ""
		return r.Banks.SaveConnection(ctx, userID, conn, s.clock.Now())
	})
	if err != nil {
		return "", err
	}
	s.CancelLogin(userID, bank)
	s.log.InfoContext(ctx, "bank connected", slog.Int64("user_id", userID), slog.String("bank", string(bank)))
	return "", nil
}

// CancelLogin drops a sign-in to bank in progress.
func (s *Banks) CancelLogin(userID int64, bank domain.Bank) {
	s.mu.Lock()
	delete(s.pending, pendingBankKey{userID, bank})
	s.mu.Unlock()
}

// Disconnect forgets the session of bank. Synced operations, the bank accounts and
// their links are kept for the next sign-in.
func (s *Banks) Disconnect(ctx context.Context, userID int64, bank domain.Bank) error {
	s.CancelLogin(userID, bank)
	if err := s.store.Write().Banks.DeleteConnection(ctx, userID, bank); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "bank disconnected", slog.Int64("user_id", userID), slog.String("bank", string(bank)))
	return nil
}

// Link makes each account of links sync with its account at bank, of the same
// currency, for good, creating the accounts of links without one, and imports the
// operations of those bank accounts in one run, so transfers between them pair: of an
// account with operations since the day after its last one, else all, and then sets
// its opening balance so the balance matches the bank. Neither side of a link may sync
// with another one already. Times are read in loc, and the operations go to the
// categories named uncategorized before rules run.
//
//nolint:cyclop,funlen,nestif // one transaction checking each link, then the sync
func (s *Banks) Link(
	ctx context.Context, userID int64, bank domain.Bank, links []domain.BankLink, loc *time.Location,
	uncategorized string,
) (domain.BankSync, error) {
	c, err := s.client(bank)
	if err != nil {
		return domain.BankSync{}, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _bankSyncTimeout)
	defer cancel()
	unlock := s.lock(userID)
	defer unlock()

	conn, err := s.Connection(ctx, userID, bank)
	if err != nil {
		return domain.BankSync{}, err
	}
	if conn.Status == domain.BankLoginRequired {
		return domain.BankSync{NeedsLogin: []domain.Bank{bank}}, nil
	}
	now := s.clock.Now()
	var (
		// starts are the starts of the syncs by the ids of the bank accounts at the bank
		starts = make(map[string]time.Time, len(links))
		// fresh are the accounts without operations, by id, set to the bank balance
		fresh   = make(map[int64]bool)
		created []int64
	)
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		accounts, err := r.Banks.Accounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, l := range links {
			i := slices.IndexFunc(accounts, func(a domain.BankAccount) bool {
				return a.ID == l.BankAccountID && a.Bank == bank
			})
			if i < 0 {
				return domain.NewError(errors.ErrInvalid, "bank account: choose from the list")
			}
			t := accounts[i]
			if t.AccountID != 0 {
				return domain.NewError(errors.ErrConflict, "bank account: already synced with another account")
			}
			// all operations, as for an account without operations
			start := time.Unix(0, 0)
			if l.AccountID == 0 {
				a, err := domain.NewAccount(domain.AccountInput{
					Name: t.Name, Currency: t.Currency, Kind: t.Kind, CreditLimit: t.CreditLimit,
				})
				if err != nil {
					return err
				}
				if a, err = r.Accounts.Create(ctx, userID, a, now); err != nil {
					return err
				}
				l.AccountID, fresh[a.ID], created = a.ID, true, append(created, a.ID)
			} else {
				if slices.ContainsFunc(accounts, func(a domain.BankAccount) bool { return a.AccountID == l.AccountID }) {
					return domain.NewError(errors.ErrConflict, "the account syncs with a bank account already")
				}
				a, err := r.Accounts.Get(ctx, userID, l.AccountID)
				if err != nil {
					return err
				}
				if a.Currency != t.Currency {
					return domain.NewError(errors.ErrInvalid, "bank account: is in %s, the account is in %s",
						t.Currency, a.Currency)
				}
				ops, err := r.Operations.List(ctx, userID, domain.Filter{AccountID: a.ID}, pagination.Cursor{}, 1)
				if err != nil {
					return err
				}
				if len(ops) == 0 {
					fresh[a.ID] = true
				} else {
					last := ops[0].Date
					start = time.Date(last.Year(), last.Month(), last.Day()+1, 0, 0, 0, 0, loc)
				}
			}
			if err := r.Banks.Link(ctx, userID, t.ID, l.AccountID, now); err != nil {
				return err
			}
			// Commit maps records by name, and CSV exports of the account map with it too
			if err := r.Imports.MapAccount(ctx, userID, bank.ImportSource(), t.Name, t.Currency, l.AccountID); err != nil {
				return err
			}
			// later links of this call see this one
			accounts[i].AccountID = l.AccountID
			starts[t.ExternalID] = start
		}
		return nil
	})
	if err != nil {
		return domain.BankSync{}, err
	}
	for _, id := range created {
		s.log.DebugContext(ctx, "account created", slog.Int64("user_id", userID), slog.Int64("account_id", id))
	}
	s.log.InfoContext(ctx, "bank accounts linked", slog.Int64("user_id", userID), slog.String("bank", string(bank)),
		slog.Int("count", len(links)))

	n, sums, err := s.run(ctx, userID, c, loc, uncategorized, &conn, starts, now)
	res, err := s.finish(ctx, userID, &conn, now, n, err)
	if err != nil || len(res.NeedsLogin) > 0 {
		return res, err
	}
	// the opening balance, before the first imported day, is the balance now less the
	// imported operations
	accounts, err := s.store.Read().Banks.Accounts(ctx, userID)
	if err != nil {
		return res, err
	}
	for _, t := range accounts {
		if !fresh[t.AccountID] {
			continue
		}
		date := timex.Today(s.clock, loc)
		if sum := sums[t.AccountID]; !sum.first.IsZero() {
			date = timex.Date(sum.first.In(loc)).AddDate(0, 0, -1)
		}
		if _, err := s.accounts.SetBalance(ctx, userID, t.AccountID, t.Balance.Sub(sums[t.AccountID].sum), date); err != nil {
			return res, err
		}
	}
	return res, nil
}

// Sync reads the accounts at bank and imports the operations of the linked ones since
// each was synced last, into the categories named uncategorized before rules run.
// Unless force is set it does nothing within SyncInterval of the last sync, nor
// without a connection or when the bank is disabled. Times are read in loc. The sync
// goes on when ctx is canceled, as the session it rotates must be stored.
func (s *Banks) Sync(
	ctx context.Context, userID int64, bank domain.Bank, loc *time.Location, uncategorized string, force bool,
) (domain.BankSync, error) {
	c, err := s.client(bank)
	if err != nil {
		// a disabled bank has nothing to sync
		return domain.BankSync{}, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _bankSyncTimeout)
	defer cancel()
	unlock := s.lock(userID)
	defer unlock()

	conn, err := s.Connection(ctx, userID, bank)
	if errors.Is(err, errors.ErrNotFound) {
		return domain.BankSync{}, nil
	}
	if err != nil {
		return domain.BankSync{}, err
	}
	if conn.Status == domain.BankLoginRequired {
		return domain.BankSync{NeedsLogin: []domain.Bank{bank}}, nil
	}
	now := s.clock.Now()
	if !force && now.Sub(conn.LastSyncAt) < s.opts.SyncInterval {
		return domain.BankSync{}, nil
	}
	accounts, err := s.store.Read().Banks.Accounts(ctx, userID)
	if err != nil {
		return domain.BankSync{}, err
	}
	starts := make(map[string]time.Time)
	for _, t := range accounts {
		if t.Bank != bank || t.AccountID == 0 {
			continue
		}
		starts[t.ExternalID] = now.Add(-s.opts.SyncOverlap)
		if !t.SyncedUntil.IsZero() {
			starts[t.ExternalID] = t.SyncedUntil.Add(-s.opts.SyncOverlap)
		}
	}
	n, _, err := s.run(ctx, userID, c, loc, uncategorized, &conn, starts, now)
	return s.finish(ctx, userID, &conn, now, n, err)
}

// SyncAll syncs every enabled bank as Sync does, one after another. A failing bank
// does not stop the others; its error is returned with the name of the bank.
func (s *Banks) SyncAll(
	ctx context.Context, userID int64, loc *time.Location, uncategorized string, force bool,
) (domain.BankSync, error) {
	var (
		all  domain.BankSync
		errs []error
	)
	for _, bank := range s.Enabled() {
		res, err := s.Sync(ctx, userID, bank, loc, uncategorized, force)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", bank.Name(), err))
		}
		all.NeedsLogin = append(all.NeedsLogin, res.NeedsLogin...)
		all.Operations += res.Operations
	}
	return all, errors.Join(errs...)
}

// lock locks the syncs and sign-ins of the user and returns the unlock func.
func (s *Banks) lock(userID int64) func() {
	s.mu.Lock()
	l, ok := s.locks[userID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[userID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// finish records the outcome of a sync that created n operations in conn: an expired
// session asks the user to sign in again, any other failure is kept to show.
func (s *Banks) finish(
	ctx context.Context, userID int64, conn *domain.BankConnection, now time.Time, n int, err error,
) (domain.BankSync, error) {
	res := domain.BankSync{Operations: n}
	conn.LastSyncAt, conn.LastError = now, ""
	attrs := []any{slog.Int64("user_id", userID), slog.String("bank", string(conn.Bank))}
	switch {
	case errors.Is(err, domain.ErrBankLoginRequired):
		conn.Status, res, err = domain.BankLoginRequired, domain.BankSync{NeedsLogin: []domain.Bank{conn.Bank}}, nil
		s.log.InfoContext(ctx, "bank session expired", attrs...)
	case err != nil:
		conn.LastError = err.Error()
		s.log.WarnContext(ctx, "bank sync failed", append(attrs, slog.Any("error", err))...)
	default:
		s.log.InfoContext(ctx, "bank synced", append(attrs, slog.Int("count", n),
			slog.Duration("took", time.Since(now)))...)
	}
	if saveErr := s.store.Write().Banks.SaveConnection(ctx, userID, *conn, now); saveErr != nil {
		return res, errors.Join(err, saveErr)
	}
	return res, err
}

// run refreshes the session of conn, stores the accounts at its bank and imports the
// operations of the bank accounts in starts, by their ids at the bank, since their
// starts, all at once: a transfer with an own account, else an expense or income in
// the categories named uncategorized, which rules then change. It returns the number
// of operations created and the sums imported by the account they sync into.
func (s *Banks) run(
	ctx context.Context, userID int64, c BankClient, loc *time.Location, uncategorized string,
	conn *domain.BankConnection, starts map[string]time.Time, now time.Time,
) (int, map[int64]bankSum, error) {
	sess, err := s.open(userID, conn.Secret)
	if err != nil {
		// sealed with another key: the user signs in again
		return 0, nil, domain.ErrBankLoginRequired
	}
	// the old session may stop working, so the new one is stored before anything else
	// can fail
	if sess, err = c.Refresh(ctx, sess); err != nil {
		return 0, nil, err
	}
	if conn.Secret, err = s.seal(userID, sess); err != nil {
		return 0, nil, err
	}
	if err := s.store.Write().Banks.SaveConnection(ctx, userID, *conn, now); err != nil {
		return 0, nil, err
	}

	// the holds of the overlap are read even with nothing linked, as balances are less them
	from := now.Add(-s.opts.SyncOverlap)
	for _, start := range starts {
		if start.Before(from) {
			from = start
		}
	}
	data, err := c.Fetch(ctx, sess, from, now, loc)
	if err != nil {
		return 0, nil, err
	}
	accounts, err := s.saveAccounts(ctx, userID, conn.Bank, data.Accounts, now)
	if err != nil || len(starts) == 0 {
		return 0, nil, err
	}
	parsed, sums := syncRecords(accounts, data.Records, starts)
	filename := conn.Bank.Name() + " " + now.In(loc).Format(time.DateTime)
	im, err := s.imports.stage(ctx, userID, conn.Bank.ImportSource(), filename, parsed)
	if err != nil {
		return 0, nil, err
	}
	var n int
	if im.RowsNew == 0 {
		if err := s.imports.Discard(ctx, userID, im.ID); err != nil {
			return 0, nil, err
		}
	} else if n, err = s.imports.commit(ctx, userID, im.ID, loc, uncategorized); err != nil {
		// the next sync stages the operations again
		return 0, nil, errors.Join(err, s.imports.Discard(ctx, userID, im.ID))
	}
	return n, sums, s.store.InTx(ctx, func(r repository.Repos) error {
		for _, t := range accounts {
			// a period synced before is not synced again
			if _, ok := starts[t.ExternalID]; ok && now.After(t.SyncedUntil) {
				if err := r.Banks.SetSyncedUntil(ctx, userID, t.ID, now, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// syncRecords returns the records to import: those of the bank accounts in starts
// since their starts, and of the bank accounts not linked whose transfers go to an own
// account, those that are the other side of a transfer with one of them, so the two
// pair into a transfer. It returns them with the sums of the former by the account
// they sync into.
func syncRecords(
	accounts []domain.BankAccount, records map[string][]domain.Record, starts map[string]time.Time,
) (domain.ParseResult, map[int64]bankSum) {
	var (
		res  domain.ParseResult
		sums = make(map[int64]bankSum)
		// others are the records of accounts not linked, kept if they pair
		others []domain.Record
	)
	for _, t := range accounts {
		start, synced := starts[t.ExternalID]
		for _, r := range records[t.ExternalID] {
			switch {
			case synced && !r.Time.Before(start):
				res.Rows = append(res.Rows, r)
				sum := sums[t.AccountID]
				sum.sum = sum.sum.Add(r.Amount)
				if sum.first.IsZero() || r.Time.Before(sum.first) {
					sum.first = r.Time
				}
				sums[t.AccountID] = sum
			case !synced && t.AccountID == 0 && t.TransfersTo != 0:
				others = append(others, r)
			}
		}
	}
	// each record of a synced account is the side of one transfer at most
	n := len(res.Rows)
	used := make([]bool, n)
	for _, o := range others {
		for i, r := range res.Rows[:n] {
			if !used[i] && r.Currency == o.Currency && r.Amount.Equal(o.Amount.Neg()) &&
				r.Time.Sub(o.Time).Abs() <= domain.TransferWindow {
				used[i] = true
				res.Rows = append(res.Rows, o)
				break
			}
		}
	}
	return res, sums
}

// saveAccounts stores the accounts at bank and maps the linked ones by their current
// names, as a renamed account must still map. It returns the stored accounts at bank.
func (s *Banks) saveAccounts(
	ctx context.Context, userID int64, bank domain.Bank, accounts []domain.BankAccount, now time.Time,
) ([]domain.BankAccount, error) {
	var stored []domain.BankAccount
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		for _, t := range accounts {
			t.Bank = bank
			if err := r.Banks.SaveAccount(ctx, userID, t, now); err != nil {
				return err
			}
		}
		all, err := r.Banks.Accounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, t := range all {
			if t.Bank != bank || t.AccountID == 0 {
				continue
			}
			if err := r.Imports.MapAccount(ctx, userID, bank.ImportSource(), t.Name, t.Currency, t.AccountID); err != nil {
				return err
			}
		}
		stored, err = bankAccounts(ctx, r, userID, bank)
		return err
	})
	return stored, err
}

// seal encrypts a session of the user; the user id binds it to the row.
func (s *Banks) seal(userID int64, sess []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("bank session nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, sess, []byte(strconv.FormatInt(userID, 10))), nil
}

// open decrypts a session sealed by seal.
func (s *Banks) open(userID int64, secret []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(secret) < n {
		return nil, errors.Error("bank session: too short")
	}
	b, err := s.aead.Open(nil, secret[:n], secret[n:], []byte(strconv.FormatInt(userID, 10)))
	if err != nil {
		return nil, fmt.Errorf("open bank session: %w", err)
	}
	return b, nil
}
