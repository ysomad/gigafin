package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

// Accounts manages accounts and their balances.
type Accounts struct {
	store *repository.Store
	clock timex.Clock
	log   *slog.Logger
}

// List returns all accounts of the user with balances, active first.
func (s *Accounts) List(ctx context.Context, userID int64) ([]domain.Account, error) {
	return s.store.Read().Accounts.List(ctx, userID)
}

// Get returns one account with its balance.
func (s *Accounts) Get(ctx context.Context, userID, id int64) (domain.Account, error) {
	return s.store.Read().Accounts.Get(ctx, userID, id)
}

// Create creates an account.
func (s *Accounts) Create(ctx context.Context, userID int64, in domain.AccountInput) (domain.Account, error) {
	a, err := domain.NewAccount(in)
	if err != nil {
		return domain.Account{}, err
	}
	a, err = s.store.Write().Accounts.Create(ctx, userID, a, s.clock.Now())
	if err != nil {
		return domain.Account{}, err
	}
	s.log.DebugContext(ctx, "account created", slog.Int64("user_id", userID), slog.Int64("account_id", a.ID))
	return a, nil
}

// CreateWithBalance creates an account and records its opening balance as of date in
// one transaction. A zero balance records nothing.
func (s *Accounts) CreateWithBalance(
	ctx context.Context, userID int64, in domain.AccountInput, balance money.Amount, date time.Time,
) (domain.Account, error) {
	a, err := domain.NewAccount(in)
	if err != nil {
		return domain.Account{}, err
	}
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		now := s.clock.Now()
		var err error
		if a, err = r.Accounts.Create(ctx, userID, a, now); err != nil {
			return err
		}
		adj, ok, err := a.BalanceAdjustment(balance, date)
		if err != nil || !ok {
			return err
		}
		_, err = createOperation(ctx, r, userID, adj, domain.ImportRef{}, now)
		return err
	})
	if err != nil {
		return a, err
	}
	s.log.DebugContext(ctx, "account created", slog.Int64("user_id", userID), slog.Int64("account_id", a.ID))
	return a, nil
}

// Update changes the account name, kind and credit limit. in.Currency is ignored.
func (s *Accounts) Update(ctx context.Context, userID, id int64, in domain.AccountInput) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		a, err := r.Accounts.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if a, err = a.Edit(in); err != nil {
			return err
		}
		return r.Accounts.Update(ctx, userID, a)
	})
	if err != nil {
		return err
	}
	s.log.DebugContext(ctx, "account updated", slog.Int64("user_id", userID), slog.Int64("account_id", id))
	return nil
}

// Archive hides or shows an account in pickers. History is kept.
func (s *Accounts) Archive(ctx context.Context, userID, id int64, archived bool) error {
	var at time.Time
	if archived {
		at = s.clock.Now()
	}
	if err := s.store.Write().Accounts.SetArchived(ctx, userID, id, at); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "account archived", slog.Int64("user_id", userID), slog.Int64("account_id", id),
		slog.Bool("archived", archived))
	return nil
}

// Delete deletes an account that has no operations.
func (s *Accounts) Delete(ctx context.Context, userID, id int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		a, err := r.Accounts.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		n, err := r.Accounts.CountOperations(ctx, id)
		if err != nil {
			return err
		}
		if err := a.CheckDelete(n); err != nil {
			return err
		}
		return r.Accounts.Delete(ctx, userID, id)
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "account deleted", slog.Int64("user_id", userID), slog.Int64("account_id", id))
	return nil
}

// SetBalance records an adjustment so that the account balance at the end of date
// becomes target; operations after date still change it. It returns the zero
// Operation when the balance already matches.
func (s *Accounts) SetBalance(
	ctx context.Context, userID, id int64, target money.Amount, date time.Time,
) (domain.Operation, error) {
	var op domain.Operation
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		a, err := r.Accounts.Get(ctx, userID, id)
		if err != nil {
			return err
		}
		if a.Balance, err = r.Accounts.BalanceAt(ctx, a, date); err != nil {
			return err
		}
		in, ok, err := a.BalanceAdjustment(target, date)
		if err != nil || !ok {
			return err
		}
		op, err = createOperation(ctx, r, userID, in, domain.ImportRef{}, s.clock.Now())
		return err
	})
	if err != nil {
		return op, err
	}
	if op.ID != 0 {
		s.log.DebugContext(ctx, "account balance adjusted", slog.Int64("user_id", userID),
			slog.Int64("account_id", id), slog.Int64("operation_id", op.ID))
	}
	return op, nil
}
