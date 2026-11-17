package domain

import (
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// Bank is a bank whose accounts a user syncs operations from.
type Bank string

const BankTBank Bank = "tbank"

// Name returns the name of the bank to show.
func (b Bank) Name() string {
	switch b {
	case BankTBank:
		return "T-Bank"
	default:
		return string(b)
	}
}

// ImportSource returns the source of the imports the bank's syncs stage, which map
// records to accounts and categories as CSV exports of the bank do.
func (b Bank) ImportSource() ImportSource {
	return ImportSource(b)
}

// ErrBankLoginRequired is a session the bank no longer accepts; the user must sign in
// again.
const ErrBankLoginRequired errors.Error = "bank session expired"

// Inputs a bank may ask for while signing in, as BankLogin.Step. A bank may ask for
// others, which are asked for as text.
const (
	BankStepOTP      = "otp"
	BankStepPassword = "password"
)

// BankLogin is a sign-in to a bank in progress.
type BankLogin interface {
	// Step is the input the bank asks for next.
	Step() string
}

// BankData is what a bank has for a period: its accounts as of now and the operations
// of the period to import.
type BankData struct {
	// Accounts are the accounts gigafin can sync with, without ID and AccountID. Their
	// Balance is less holds not yet debited, as those are not imported until they are.
	Accounts []BankAccount
	// Records are the operations to import by the ExternalID of their account, as the
	// rows of a CSV export of the bank, so both dedupe against each other. Operations
	// that never import, such as failed ones or holds, are left out.
	Records map[string][]Record
}

// BankStatus is the state of a connection to a bank.
type BankStatus string

const (
	BankConnected BankStatus = "connected"
	// BankLoginRequired is a connection whose session the bank no longer accepts.
	BankLoginRequired BankStatus = "login_required"
)

// BankConnection is a user's sign-in to a bank. Login is what the user signs in with,
// such as a phone, and Secret the sealed session.
type BankConnection struct {
	Bank       Bank
	Login      string
	Status     BankStatus
	Secret     []byte
	LastSyncAt time.Time
	// LastError is the failure of the last sync, empty when it succeeded.
	LastError string
}

// BankAccount is an account at a bank as of the last sign-in or sync, and the account
// it syncs with. An account syncs with one bank account at most, chosen when the
// account is created.
type BankAccount struct {
	ID   int64
	Bank Bank
	// ExternalID is the id of the account at the bank.
	ExternalID string
	Name       string
	Currency   string
	Kind       AccountKind
	// Balance is the balance the bank shows less holds not yet debited, which are not
	// imported until they are, so it compares with the balance of the linked account.
	Balance money.Amount
	// CreditLimit is the limit of a credit card, zero for other accounts.
	CreditLimit money.Amount
	// AccountID is the linked account, zero when the bank account is not synced.
	AccountID int64
	// SyncedUntil is the end of the last synced period, zero before the first sync.
	SyncedUntil time.Time
	// SyncedFrom is where the sync of the linked account starts, zero when not linked.
	// Operations before it were in gigafin when it was linked.
	SyncedFrom time.Time
	// TransfersTo is the account transfers with the bank account go to when it is not
	// linked, zero when none.
	TransfersTo int64
}

// Mismatch reports whether a is the linked account and its balance differs from the
// bank's.
func (t BankAccount) Mismatch(a Account) bool {
	return t.AccountID != 0 && t.AccountID == a.ID && !t.Balance.Equal(a.Balance)
}

// BankSync is the outcome of syncing one bank or several.
type BankSync struct {
	// NeedsLogin are the banks the user must sign in to again.
	NeedsLogin []Bank
	// Operations is the number of operations created.
	Operations int
}

// BankLink links the account AccountID, a new one when zero, to sync with the bank
// account BankAccountID.
type BankLink struct {
	AccountID     int64
	BankAccountID int64
}

// NormalizePhone returns a phone number in the international form +79991234567. A
// Russian number may start with 8 or 7.
func NormalizePhone(s string) (string, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if len(digits) == 11 && digits[0] == '8' {
		digits = "7" + digits[1:]
	}
	if len(digits) < 11 || len(digits) > 15 {
		return "", NewError(errors.ErrInvalid, "phone: must be like +79991234567")
	}
	return "+" + digits, nil
}
