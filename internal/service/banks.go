package service

import (
	"context"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/repository"
)

// Banks lists what the syncs of every bank store. Signing in to a bank and syncing it
// is done by the service of that bank, such as TBank.
type Banks struct {
	store *repository.Store
	tbank *TBank
}

// Enabled returns the banks whose sync is configured.
func (s *Banks) Enabled() []domain.Bank {
	var banks []domain.Bank
	if s.tbank.Enabled() {
		banks = append(banks, domain.BankTBank)
	}
	return banks
}

// Accounts returns the user's accounts at the enabled banks as of the last sign-in or
// sync, and the accounts the transfers with the ones not linked go to.
func (s *Banks) Accounts(ctx context.Context, userID int64) ([]domain.BankAccount, error) {
	enabled := s.Enabled()
	if len(enabled) == 0 {
		return nil, nil
	}
	all, err := s.store.Read().Banks.Accounts(ctx, userID)
	if err != nil {
		return nil, err
	}
	var accounts []domain.BankAccount
	for _, bank := range enabled {
		m, err := s.store.Read().Imports.Mapping(ctx, userID, bank.ImportSource())
		if err != nil {
			return nil, err
		}
		for _, t := range all {
			if t.Bank != bank {
				continue
			}
			if a, ok := m.Accounts[domain.AccountKey(t.Name, t.Currency)]; ok && t.AccountID == 0 {
				t.TransfersTo = a.ID
			}
			accounts = append(accounts, t)
		}
	}
	return accounts, nil
}
