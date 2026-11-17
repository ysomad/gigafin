package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/importer"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/tbank"
	"github.com/ysomad/gigafin/internal/timex"
)

// _tbankLoginTTL is how long a sign-in waits for the next step.
const _tbankLoginTTL = 10 * time.Minute

// _tbankSyncTimeout bounds a sync, which goes on when the request that started it ends.
const _tbankSyncTimeout = 2 * time.Minute

// TBankClient calls the T-Bank API.
type TBankClient interface {
	StartLogin(ctx context.Context, deviceID, phone string) (*tbank.Login, error)
	Confirm(ctx context.Context, l *tbank.Login, value string) (tbank.Session, bool, error)
	Refresh(ctx context.Context, s tbank.Session) (tbank.Session, error)
	Accounts(ctx context.Context, s tbank.Session) ([]tbank.Account, error)
	Operations(ctx context.Context, s tbank.Session, from, to time.Time) ([]tbank.Operation, error)
}

// TBankOptions tune T-Bank sync. Without Client or Key it is disabled.
type TBankOptions struct {
	Client TBankClient
	// Key encrypts T-Bank sessions stored in the database, 32 bytes.
	Key []byte
	// SyncInterval is the least time between two syncs that are not forced.
	SyncInterval time.Duration
	// SyncOverlap is how far before the end of the last synced period a sync starts.
	SyncOverlap time.Duration
}

// TBank signs users in to T-Bank, keeps their T-Bank accounts and syncs the operations
// of the ones linked to accounts into imports, committed at once when every account and
// category is mapped. Balances are never changed; the bank balance is kept to compare.
type TBank struct {
	store   *repository.Store
	clock   timex.Clock
	log     *slog.Logger
	imports *Imports
	client  TBankClient
	aead    cipher.AEAD
	opts    TBankOptions

	// mu guards pending and locks
	mu      sync.Mutex
	pending map[int64]pendingTBankLogin
	// locks has a lock per user, so syncs of a user do not overlap
	locks map[int64]*sync.Mutex
}

type pendingTBankLogin struct {
	login   *tbank.Login
	phone   string
	expires time.Time
}

func newTBank(
	store *repository.Store, clock timex.Clock, log *slog.Logger, imports *Imports, opts TBankOptions,
) (*TBank, error) {
	s := &TBank{
		store: store, clock: clock, log: log, imports: imports, client: opts.Client, opts: opts,
		pending: make(map[int64]pendingTBankLogin), locks: make(map[int64]*sync.Mutex),
	}
	if opts.Client == nil || len(opts.Key) == 0 {
		return s, nil
	}
	block, err := aes.NewCipher(opts.Key)
	if err != nil {
		return nil, fmt.Errorf("t-bank secret key: %w", err)
	}
	if s.aead, err = cipher.NewGCM(block); err != nil {
		return nil, fmt.Errorf("t-bank secret key: %w", err)
	}
	return s, nil
}

// Enabled reports whether T-Bank sync is configured.
func (s *TBank) Enabled() bool {
	return s.aead != nil
}

// Connection returns the user's connection to T-Bank, errors.ErrNotFound when there
// is none.
func (s *TBank) Connection(ctx context.Context, userID int64) (domain.BankConnection, error) {
	return s.store.Read().Banks.Connection(ctx, userID, domain.BankTBank)
}

// LoginStep returns the input a sign-in of the user in progress waits for and its
// phone, empty when there is none.
func (s *TBank) LoginStep(userID int64) (step, phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[userID]
	if !ok || s.clock.Now().After(p.expires) {
		return "", ""
	}
	return p.login.Step, p.phone
}

// StartLogin starts signing the user in to T-Bank by phone; the bank sends an SMS
// code. It returns the step the sign-in waits for.
func (s *TBank) StartLogin(ctx context.Context, userID int64, phone string) (string, error) {
	if !s.Enabled() {
		return "", domain.NewError(errors.ErrConflict, "T-Bank sync is disabled")
	}
	phone, err := domain.NormalizePhone(phone)
	if err != nil {
		return "", err
	}
	// the device of the previous session is kept, so the bank does not see a new one
	var deviceID string
	if conn, err := s.Connection(ctx, userID); err == nil {
		if sess, err := s.open(userID, conn.Secret); err == nil {
			deviceID = sess.DeviceID
		}
	}
	l, err := s.client.StartLogin(ctx, deviceID, phone)
	if err != nil {
		return "", domain.NewError(errors.ErrInvalid, "the bank refused to sign in: %v", err)
	}
	s.mu.Lock()
	s.pending[userID] = pendingTBankLogin{login: l, phone: phone, expires: s.clock.Now().Add(_tbankLoginTTL)}
	s.mu.Unlock()
	s.log.InfoContext(ctx, "t-bank login started", slog.Int64("user_id", userID))
	return l.Step, nil
}

// ConfirmLogin answers the step the sign-in waits for, such as the SMS code or the
// password. It returns the next step, empty when the user is signed in; the T-Bank
// accounts are then read and stored, to create accounts with.
func (s *TBank) ConfirmLogin(ctx context.Context, userID int64, value string) (string, error) {
	s.mu.Lock()
	p, ok := s.pending[userID]
	s.mu.Unlock()
	if !ok || s.clock.Now().After(p.expires) {
		return "", domain.NewError(errors.ErrInvalid, "sign-in expired, start again")
	}
	sess, done, err := s.client.Confirm(ctx, p.login, value)
	if err != nil {
		return p.login.Step, domain.NewError(errors.ErrInvalid, "the bank did not accept it: %v", err)
	}
	if !done {
		return p.login.Step, nil
	}
	secret, err := s.seal(userID, sess)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		conn, err := r.Banks.Connection(ctx, userID, domain.BankTBank)
		if err != nil && !errors.Is(err, errors.ErrNotFound) {
			return err
		}
		conn.Bank, conn.Login, conn.Status, conn.Secret, conn.LastError =
			domain.BankTBank, p.phone, domain.BankConnected, secret, ""
		return r.Banks.SaveConnection(ctx, userID, conn, now)
	})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	delete(s.pending, userID)
	s.mu.Unlock()
	s.log.InfoContext(ctx, "t-bank connected", slog.Int64("user_id", userID))

	// a failure is retried when the user opens the app next
	accounts, err := s.client.Accounts(ctx, sess)
	if err == nil {
		err = s.saveAccounts(ctx, userID, accounts, nil /* ops */, nil /* linked */, now)
	}
	if err != nil {
		s.log.WarnContext(ctx, "t-bank accounts not read", slog.Int64("user_id", userID), slog.Any("error", err))
	}
	return "", nil
}

// CancelLogin drops a sign-in in progress.
func (s *TBank) CancelLogin(userID int64) {
	s.mu.Lock()
	delete(s.pending, userID)
	s.mu.Unlock()
}

// Disconnect forgets the session. Synced operations, the T-Bank accounts and their
// links are kept for the next sign-in.
func (s *TBank) Disconnect(ctx context.Context, userID int64) error {
	s.CancelLogin(userID)
	if err := s.store.Write().Banks.DeleteConnection(ctx, userID, domain.BankTBank); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "t-bank disconnected", slog.Int64("user_id", userID))
	return nil
}

// Link makes each account of links sync with its T-Bank account, of the same currency,
// for good, and imports the operations of those T-Bank accounts, each since the From of
// its link, in one run, so transfers between them pair. Neither side of a link may sync
// with another one already. Times are read in loc, and the operations go to the
// categories named uncategorized before rules run.
func (s *TBank) Link(
	ctx context.Context, userID int64, links []domain.BankLink, loc *time.Location, uncategorized string,
) (domain.BankSync, error) {
	if !s.Enabled() {
		return domain.BankSync{}, domain.NewError(errors.ErrConflict, "T-Bank sync is disabled")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _tbankSyncTimeout)
	defer cancel()
	unlock := s.lock(userID)
	defer unlock()

	now := s.clock.Now()
	var (
		linked []string
		// starts are the From of the links by the T-Bank account ids, from the earliest
		starts = make(map[string]time.Time, len(links))
		from   = now
	)
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		accounts, err := r.Banks.Accounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, l := range links {
			i := slices.IndexFunc(accounts, func(a domain.BankAccount) bool {
				return a.ID == l.BankAccountID && a.Bank == domain.BankTBank
			})
			if i < 0 {
				return domain.NewError(errors.ErrInvalid, "bank account: choose from the list")
			}
			t := accounts[i]
			if t.AccountID != 0 {
				return domain.NewError(errors.ErrConflict, "bank account: already synced with another account")
			}
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
			if err := r.Banks.Link(ctx, userID, t.ID, l.AccountID, now); err != nil {
				return err
			}
			// Commit maps records by name, and CSV exports of the account map with it too
			if err := r.Imports.MapAccount(ctx, userID, domain.ImportSourceTBank, t.Name, t.Currency, l.AccountID); err != nil {
				return err
			}
			// later links of this call see this one
			accounts[i].AccountID = l.AccountID
			linked, starts[t.ExternalID] = append(linked, t.ExternalID), l.From
			if l.From.Before(from) {
				from = l.From
			}
			s.log.InfoContext(ctx, "t-bank account linked", slog.Int64("user_id", userID),
				slog.Int64("account_id", l.AccountID), slog.String("tbank_account_id", t.ExternalID), slog.Time("from", l.From))
		}
		return nil
	})
	if err != nil {
		return domain.BankSync{}, err
	}

	conn, err := s.Connection(ctx, userID)
	if err != nil {
		return domain.BankSync{}, err
	}
	if conn.Status == domain.BankLoginRequired {
		return domain.BankSync{NeedsLogin: true}, nil
	}
	res, err := s.run(ctx, userID, loc, uncategorized, &conn, linked, starts, from, now)
	return s.finish(ctx, userID, &conn, now, res, err)
}

// Sync reads the T-Bank accounts and imports the operations of the linked ones since
// each was synced last, into the categories named uncategorized before rules run.
// Unless force is set it does nothing within SyncInterval of the last sync. A user
// without a connection gets Skipped; one whose session expired gets NeedsLogin. Times
// are read in loc. The sync goes on when ctx is canceled, as the session it rotates must
// be stored.
func (s *TBank) Sync(
	ctx context.Context, userID int64, loc *time.Location, uncategorized string, force bool,
) (domain.BankSync, error) {
	if !s.Enabled() {
		return domain.BankSync{Skipped: true}, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), _tbankSyncTimeout)
	defer cancel()
	unlock := s.lock(userID)
	defer unlock()

	conn, err := s.Connection(ctx, userID)
	if errors.Is(err, errors.ErrNotFound) {
		return domain.BankSync{Skipped: true}, nil
	}
	if err != nil {
		return domain.BankSync{}, err
	}
	if conn.Status == domain.BankLoginRequired {
		s.log.DebugContext(ctx, "t-bank sync skipped: login required", slog.Int64("user_id", userID))
		return domain.BankSync{NeedsLogin: true}, nil
	}
	now := s.clock.Now()
	if !force && now.Sub(conn.LastSyncAt) < s.opts.SyncInterval {
		s.log.DebugContext(ctx, "t-bank sync skipped: synced recently", slog.Int64("user_id", userID),
			slog.Time("last_sync_at", conn.LastSyncAt))
		return domain.BankSync{Skipped: true}, nil
	}
	accounts, err := s.store.Read().Banks.Accounts(ctx, userID)
	if err != nil {
		return domain.BankSync{}, err
	}
	var linked []string
	from := now
	for _, t := range accounts {
		if t.Bank != domain.BankTBank || t.AccountID == 0 {
			continue
		}
		linked = append(linked, t.ExternalID)
		start := t.SyncedUntil.Add(-s.opts.SyncOverlap)
		if t.SyncedUntil.IsZero() {
			start = now.Add(-s.opts.SyncOverlap)
		}
		if start.Before(from) {
			from = start
		}
	}
	res, err := s.run(ctx, userID, loc, uncategorized, &conn, linked, nil /* starts */, from, now)
	return s.finish(ctx, userID, &conn, now, res, err)
}

// lock locks the syncs of the user and returns the unlock func.
func (s *TBank) lock(userID int64) func() {
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

// finish records the outcome of a sync in conn: an expired session asks the user to
// sign in again, any other failure is kept to show.
func (s *TBank) finish(
	ctx context.Context, userID int64, conn *domain.BankConnection, now time.Time, res domain.BankSync, err error,
) (domain.BankSync, error) {
	conn.LastSyncAt, conn.LastError = now, ""
	switch {
	case errors.Is(err, tbank.ErrSessionExpired):
		conn.Status, res, err = domain.BankLoginRequired, domain.BankSync{NeedsLogin: true}, nil
		s.log.InfoContext(ctx, "t-bank session expired", slog.Int64("user_id", userID))
	case err != nil:
		conn.LastError = err.Error()
		s.log.WarnContext(ctx, "t-bank sync failed", slog.Int64("user_id", userID), slog.Any("error", err))
	default:
		s.log.InfoContext(ctx, "t-bank synced", slog.Int64("user_id", userID), slog.Int("operations", res.Operations),
			slog.Duration("took", time.Since(now)))
	}
	if saveErr := s.store.Write().Banks.SaveConnection(ctx, userID, *conn, now); saveErr != nil {
		return res, errors.Join(err, saveErr)
	}
	return res, err
}

// run reads the T-Bank accounts of the user and imports the operations of the ones
// linked, by their ids at the bank, between from and now, all of them at once: a
// transfer with an own account, else an expense or income in the categories named
// uncategorized, which rules then change. Operations of a T-Bank account in starts
// before its start are not imported. With none linked it only reads the accounts. It
// updates the T-Bank accounts and conn.
//
//nolint:cyclop,funlen // one linear sequence of calls, each checked and logged
func (s *TBank) run(
	ctx context.Context, userID int64, loc *time.Location, uncategorized string, conn *domain.BankConnection,
	linked []string, starts map[string]time.Time, from, now time.Time,
) (domain.BankSync, error) {
	s.log.InfoContext(ctx, "t-bank sync started", slog.Int64("user_id", userID), slog.Int("accounts", len(linked)),
		slog.Time("from", from))
	sess, err := s.open(userID, conn.Secret)
	if err != nil {
		// sealed with another key: the user signs in again
		return domain.BankSync{}, tbank.ErrSessionExpired
	}
	// the old refresh token stops working, so the new session is stored before
	// anything else can fail
	if sess, err = s.client.Refresh(ctx, sess); err != nil {
		return domain.BankSync{}, err
	}
	if conn.Secret, err = s.seal(userID, sess); err != nil {
		return domain.BankSync{}, err
	}
	if err := s.store.Write().Banks.SaveConnection(ctx, userID, *conn, now); err != nil {
		return domain.BankSync{}, err
	}
	s.log.DebugContext(ctx, "t-bank session refreshed", slog.Int64("user_id", userID),
		slog.Time("expires_at", sess.ExpiresAt))

	accounts, err := s.client.Accounts(ctx, sess)
	if err != nil {
		return domain.BankSync{}, err
	}
	if len(linked) == 0 {
		return domain.BankSync{}, s.saveAccounts(ctx, userID, accounts, nil /* ops */, nil /* linked */, now)
	}
	ops, err := s.client.Operations(ctx, sess, from, now)
	if err != nil {
		return domain.BankSync{}, err
	}
	s.log.DebugContext(ctx, "t-bank data fetched", slog.Int64("user_id", userID), slog.Int("accounts", len(accounts)),
		slog.Int("operations", len(ops)), slog.Time("from", from), slog.Time("to", now))
	if err := s.saveAccounts(ctx, userID, accounts, ops, linked, now); err != nil {
		return domain.BankSync{}, err
	}

	// transfers with the accounts not linked but mapped to own accounts are imported
	// as transfers to them
	cached, err := s.store.Read().Banks.Accounts(ctx, userID)
	if err != nil {
		return domain.BankSync{}, err
	}
	m, err := s.store.Read().Imports.Mapping(ctx, userID, domain.ImportSourceTBank)
	if err != nil {
		return domain.BankSync{}, err
	}
	var mapped []string
	for _, t := range cached {
		if _, ok := m.Accounts[domain.AccountKey(t.Name, t.Currency)]; ok && t.Bank == domain.BankTBank && t.AccountID == 0 {
			mapped = append(mapped, t.ExternalID)
		}
	}
	var tracked []tbank.Account
	for _, a := range accounts {
		if a.Tracked() {
			tracked = append(tracked, a)
		}
	}
	// an account linked with operations of its own syncs since after them, before the
	// transfers pair
	ops = slices.DeleteFunc(ops, func(op tbank.Operation) bool {
		start, ok := starts[op.Account]
		return ok && op.Time().Before(start)
	})
	parsed := importer.FromTBank(tracked, linked, mapped, ops, loc)
	if len(parsed.Skipped) > 0 {
		reasons := map[string]int{}
		for _, r := range parsed.Skipped {
			reasons[r.SkipReason]++
		}
		attrs := []any{slog.Int64("user_id", userID)}
		for reason, n := range reasons {
			attrs = append(attrs, slog.Int(reason, n))
		}
		s.log.DebugContext(ctx, "t-bank operations skipped", attrs...)
	}
	res := domain.BankSync{Fetched: make(map[int64]money.Amount)}
	// the other sides of transfers belong to accounts not linked
	for _, r := range parsed.Rows {
		i := slices.IndexFunc(tracked, func(a tbank.Account) bool {
			return a.Name == r.Account && slices.Contains(linked, a.ID)
		})
		if i < 0 {
			continue
		}
		if j := slices.IndexFunc(cached, func(t domain.BankAccount) bool {
			return t.Bank == domain.BankTBank && t.ExternalID == tracked[i].ID
		}); j >= 0 {
			id := cached[j].AccountID
			res.Fetched[id] = res.Fetched[id].Add(r.Amount)
		}
	}
	filename := "T-Bank " + now.In(loc).Format(time.DateTime)
	im, err := s.imports.stage(ctx, userID, domain.ImportSourceTBank, filename, parsed)
	if err != nil {
		return res, err
	}
	if im.RowsNew == 0 {
		if err := s.imports.Discard(ctx, userID, im.ID); err != nil {
			return res, err
		}
		s.log.DebugContext(ctx, "t-bank sync found nothing new", slog.Int64("user_id", userID))
	} else if res.Operations, err = s.imports.commit(ctx, userID, im.ID, loc, uncategorized); err != nil {
		// the next sync stages the operations again
		if discardErr := s.imports.Discard(ctx, userID, im.ID); discardErr != nil {
			return res, errors.Join(err, discardErr)
		}
		return res, err
	}
	return res, s.store.InTx(ctx, func(r repository.Repos) error {
		cached, err := r.Banks.Accounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, t := range cached {
			// a period synced before is not synced again
			if t.Bank == domain.BankTBank && slices.Contains(linked, t.ExternalID) && now.After(t.SyncedUntil) {
				if err := r.Banks.SetSyncedUntil(ctx, userID, t.ID, now, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// saveAccounts stores the tracked T-Bank accounts with their balances less the holds
// among ops not yet debited, and maps the linked ones, by their ids at the bank, by
// their current names, as a renamed account must still map.
func (s *TBank) saveAccounts(
	ctx context.Context, userID int64, accounts []tbank.Account, ops []tbank.Operation, linked []string, now time.Time,
) error {
	return s.store.InTx(ctx, func(r repository.Repos) error {
		cached, err := r.Banks.Accounts(ctx, userID)
		if err != nil {
			return err
		}
		for _, a := range accounts {
			if !a.Tracked() {
				continue
			}
			balance := a.Balance()
			for _, op := range ops {
				if op.Account == a.ID && op.Status == tbank.OpStatusOK && op.Pending() {
					balance = balance.Sub(op.Amount())
				}
			}
			t := domain.BankAccount{
				Bank: domain.BankTBank, ExternalID: a.ID, Name: a.Name, Currency: strings.ToUpper(a.Currency.Name),
				Kind: domain.AccountDebit, Balance: balance, BalanceAt: now,
			}
			switch a.AccountType {
			case tbank.AccountSaving:
				t.Kind = domain.AccountSavings
			case tbank.AccountCredit:
				t.Kind, t.CreditLimit = domain.AccountCredit, a.CreditLimit.Value
			case tbank.AccountCashLoan:
				t.Kind = domain.AccountLoan
			}
			if err := r.Banks.SaveAccount(ctx, userID, t, now); err != nil {
				return err
			}
			i := slices.IndexFunc(cached, func(c domain.BankAccount) bool {
				return c.Bank == domain.BankTBank && c.ExternalID == a.ID
			})
			if i < 0 || cached[i].AccountID == 0 || !slices.Contains(linked, a.ID) {
				continue
			}
			if err := r.Imports.MapAccount(ctx, userID, domain.ImportSourceTBank, t.Name, t.Currency,
				cached[i].AccountID); err != nil {
				return err
			}
		}
		return nil
	})
}

// seal encrypts a session of the user; the user id binds it to the row.
func (s *TBank) seal(userID int64, sess tbank.Session) ([]byte, error) {
	b, err := json.Marshal(sess) //nolint:gosec // G117: encrypted right below
	if err != nil {
		return nil, fmt.Errorf("marshal t-bank session: %w", err)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("t-bank session nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, b, []byte(strconv.FormatInt(userID, 10))), nil
}

// open decrypts a session sealed by seal.
func (s *TBank) open(userID int64, secret []byte) (tbank.Session, error) {
	n := s.aead.NonceSize()
	if len(secret) < n {
		return tbank.Session{}, errors.Error("t-bank session: too short")
	}
	b, err := s.aead.Open(nil, secret[:n], secret[n:], []byte(strconv.FormatInt(userID, 10)))
	if err != nil {
		return tbank.Session{}, fmt.Errorf("open t-bank session: %w", err)
	}
	var sess tbank.Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return tbank.Session{}, fmt.Errorf("unmarshal t-bank session: %w", err)
	}
	return sess, nil
}
