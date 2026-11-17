package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
	"github.com/ysomad/gigafin/internal/money"
)

// Banks stores the users' connections to banks and their bank accounts.
type Banks struct {
	q *sqlc.Queries
}

// Connection returns the user's connection to bank.
func (r *Banks) Connection(ctx context.Context, userID int64, bank domain.Bank) (domain.BankConnection, error) {
	c, err := r.q.GetBankConnection(ctx, sqlc.GetBankConnectionParams{UserID: userID, Bank: string(bank)})
	if err != nil {
		return domain.BankConnection{}, notFound(err, "bank connection")
	}
	conn := domain.BankConnection{
		Bank:      domain.Bank(c.Bank),
		Login:     c.Login,
		Status:    domain.BankStatus(c.Status),
		Secret:    c.Secret,
		LastError: c.LastError,
	}
	if c.LastSyncAt.Valid {
		conn.LastSyncAt = time.Unix(c.LastSyncAt.Int64, 0)
	}
	return conn, nil
}

// SaveConnection creates or replaces the user's connection to c.Bank.
func (r *Banks) SaveConnection(ctx context.Context, userID int64, c domain.BankConnection, now time.Time) error {
	err := r.q.UpsertBankConnection(ctx, sqlc.UpsertBankConnectionParams{
		UserID:     userID,
		Bank:       string(c.Bank),
		Login:      c.Login,
		Status:     string(c.Status),
		Secret:     c.Secret,
		LastSyncAt: nullUnix(c.LastSyncAt),
		LastError:  c.LastError,
		CreatedAt:  now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("save bank connection: %w", err)
	}
	return nil
}

// DeleteConnection deletes the user's connection to bank; the bank accounts and their
// links are kept for the next sign-in.
func (r *Banks) DeleteConnection(ctx context.Context, userID int64, bank domain.Bank) error {
	if err := r.q.DeleteBankConnection(ctx, sqlc.DeleteBankConnectionParams{
		UserID: userID, Bank: string(bank),
	}); err != nil {
		return fmt.Errorf("delete bank connection: %w", err)
	}
	return nil
}

// Accounts returns the user's accounts at every bank, by bank and name.
func (r *Banks) Accounts(ctx context.Context, userID int64) ([]domain.BankAccount, error) {
	rows, err := r.q.ListBankAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list bank accounts: %w", err)
	}
	out := make([]domain.BankAccount, len(rows))
	for i, a := range rows {
		out[i] = domain.BankAccount{
			ID:          a.ID,
			Bank:        domain.Bank(a.Bank),
			ExternalID:  a.ExternalID,
			Name:        a.Name,
			Currency:    a.Currency,
			Kind:        domain.AccountKind(a.Kind),
			Balance:     money.FromMinor(a.Balance, a.Currency),
			CreditLimit: money.FromMinor(a.CreditLimit, a.Currency),
			BalanceAt:   time.Unix(a.BalanceAt, 0),
			AccountID:   a.AccountID.Int64,
		}
		if a.SyncedUntil.Valid {
			out[i].SyncedUntil = time.Unix(a.SyncedUntil.Int64, 0)
		}
	}
	return out, nil
}

// SaveAccount creates a bank account, by its bank and external id, or updates its
// name, currency, kind and balance; the link and the synced period are kept.
func (r *Banks) SaveAccount(ctx context.Context, userID int64, a domain.BankAccount, now time.Time) error {
	balance, err := a.Balance.ToMinor(a.Currency)
	if err != nil {
		return fmt.Errorf("bank account %s balance: %w", a.ExternalID, err)
	}
	limit, err := a.CreditLimit.ToMinor(a.Currency)
	if err != nil {
		return fmt.Errorf("bank account %s credit limit: %w", a.ExternalID, err)
	}
	err = r.q.UpsertBankAccount(ctx, sqlc.UpsertBankAccountParams{
		UserID:      userID,
		Bank:        string(a.Bank),
		ExternalID:  a.ExternalID,
		Name:        a.Name,
		Currency:    a.Currency,
		Kind:        string(a.Kind),
		Balance:     balance,
		CreditLimit: limit,
		BalanceAt:   a.BalanceAt.Unix(),
		CreatedAt:   now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("save bank account: %w", err)
	}
	return nil
}

// Link links the bank account id to accountID for good. It fails with
// errors.ErrConflict when the bank account syncs with an account already and
// errors.ErrExists when the account syncs with another bank account.
func (r *Banks) Link(ctx context.Context, userID, id, accountID int64, now time.Time) error {
	n, err := r.q.LinkBankAccount(ctx, sqlc.LinkBankAccountParams{
		AccountID: sql.NullInt64{Int64: accountID, Valid: true},
		UpdatedAt: sql.NullInt64{Int64: now.Unix(), Valid: true},
		UserID:    userID,
		ID:        id,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("link bank account: %w", k)
	}
	if err != nil {
		return fmt.Errorf("link bank account: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("link bank account: %w", errors.ErrConflict)
	}
	return nil
}

// SetSyncedUntil sets the end of the synced period of the bank account id.
func (r *Banks) SetSyncedUntil(ctx context.Context, userID, id int64, until, now time.Time) error {
	err := r.q.SetBankAccountSyncedUntil(ctx, sqlc.SetBankAccountSyncedUntilParams{
		SyncedUntil: nullUnix(until),
		UpdatedAt:   sql.NullInt64{Int64: now.Unix(), Valid: true},
		UserID:      userID,
		ID:          id,
	})
	if err != nil {
		return fmt.Errorf("set bank synced period: %w", err)
	}
	return nil
}

// DeleteAll deletes the user's connections and bank accounts.
func (r *Banks) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteBankAccounts(ctx, userID); err != nil {
		return fmt.Errorf("delete bank accounts: %w", err)
	}
	if err := r.q.DeleteBankConnections(ctx, userID); err != nil {
		return fmt.Errorf("delete bank connections: %w", err)
	}
	return nil
}
