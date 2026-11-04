package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/timex"
)

// Accounts stores accounts. Balances are computed from operations.
type Accounts struct {
	q *sqlc.Queries
}

// List returns all accounts of the user with balances, active first.
func (r *Accounts) List(ctx context.Context, userID int64) ([]domain.Account, error) {
	rows, err := r.q.ListAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	out := make([]domain.Account, len(rows))
	for i, row := range rows {
		out[i] = accountFromRow(row.Account, row.Balance)
	}
	return out, nil
}

// Get returns one account with its balance.
func (r *Accounts) Get(ctx context.Context, userID, id int64) (domain.Account, error) {
	row, err := r.q.GetAccount(ctx, sqlc.GetAccountParams{ID: id, UserID: userID})
	if err != nil {
		return domain.Account{}, notFound(err, "account")
	}
	return accountFromRow(row.Account, row.Balance), nil
}

// BalanceAt returns the balance of the account at the end of date.
func (r *Accounts) BalanceAt(
	ctx context.Context, a domain.Account, date time.Time,
) (money.Amount, error) {
	minor, err := r.q.GetAccountBalanceAt(ctx, sqlc.GetAccountBalanceAtParams{
		AccountID: a.ID, Date: timex.FormatDate(date),
	})
	if err != nil {
		return money.Amount{}, fmt.Errorf("get balance at: %w", err)
	}
	return money.FromMinor(minor, a.Currency), nil
}

// Create stores a new account and returns it with its id.
func (r *Accounts) Create(
	ctx context.Context, userID int64, a domain.Account, now time.Time,
) (domain.Account, error) {
	limit, err := a.CreditLimit.ToMinor(a.Currency)
	if err != nil {
		return domain.Account{}, fmt.Errorf("credit limit: %w", err)
	}
	row, err := r.q.CreateAccount(ctx, sqlc.CreateAccountParams{
		UserID:      userID,
		Name:        a.Name,
		Currency:    a.Currency,
		Kind:        string(a.Kind),
		CreditLimit: sql.NullInt64{Int64: limit, Valid: limit != 0},
		CreatedAt:   now.Unix(),
	})
	if k, ok := constraint(err); ok {
		return domain.Account{}, fmt.Errorf("account %q: %w", a.Name, k)
	}
	if err != nil {
		return domain.Account{}, fmt.Errorf("create account: %w", err)
	}
	return accountFromRow(row, 0 /* balance */), nil
}

// Update stores the name, kind and credit limit of a.
func (r *Accounts) Update(ctx context.Context, userID int64, a domain.Account) error {
	limit, err := a.CreditLimit.ToMinor(a.Currency)
	if err != nil {
		return fmt.Errorf("credit limit: %w", err)
	}
	n, err := r.q.UpdateAccount(ctx, sqlc.UpdateAccountParams{
		Name: a.Name, Kind: string(a.Kind), CreditLimit: sql.NullInt64{Int64: limit, Valid: limit != 0},
		ID: a.ID, UserID: userID,
	})
	if k, ok := constraint(err); ok {
		return fmt.Errorf("account %q: %w", a.Name, k)
	}
	return affected(n, err, "update account")
}

// SetArchived archives the account at the given time, or unarchives it for the zero
// time.
func (r *Accounts) SetArchived(ctx context.Context, userID, id int64, at time.Time) error {
	n, err := r.q.SetAccountArchived(ctx, sqlc.SetAccountArchivedParams{
		ArchivedAt: nullUnix(at), ID: id, UserID: userID,
	})
	return affected(n, err, "archive account")
}

// CountOperations counts operations from or to the account.
func (r *Accounts) CountOperations(ctx context.Context, id int64) (int64, error) {
	n, err := r.q.CountAccountOperations(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count operations: %w", err)
	}
	return n, nil
}

// Delete deletes an account.
func (r *Accounts) Delete(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteAccount(ctx, sqlc.DeleteAccountParams{ID: id, UserID: userID})
	return affected(n, err, "delete account")
}

// DeleteAll deletes every account of the user. Operations and import mappings must
// be deleted first.
func (r *Accounts) DeleteAll(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserAccounts(ctx, userID); err != nil {
		return fmt.Errorf("delete accounts: %w", err)
	}
	return nil
}

func accountFromRow(a sqlc.Account, balance int64) domain.Account {
	acc := domain.Account{
		ID:       a.ID,
		Name:     a.Name,
		Currency: a.Currency,
		Kind:     domain.AccountKind(a.Kind),
		Archived: a.ArchivedAt.Valid,
		Balance:  money.FromMinor(balance, a.Currency),
	}
	if a.CreditLimit.Valid {
		acc.CreditLimit = money.FromMinor(a.CreditLimit.Int64, a.Currency)
	}
	return acc
}
