package app

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/exchangerate"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/tbank"
	"github.com/ysomad/gigafin/internal/timex"
)

// newServices opens the database of cfg and returns the services over it, logging to log,
// with a func that closes the database.
func newServices(ctx context.Context, cfg config.Config, log *slog.Logger) (*service.Services, func(), error) {
	banks, err := newBankOptions(cfg.Banks, log)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	store, err := repository.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	svc, err := service.New(store, timex.System{}, log, service.Options{
		Session: domain.SessionLifetime{Max: cfg.Session.TTL, Idle: cfg.Session.IdleTTL},
		Import: service.ImportOptions{
			LoanPattern:    cfg.Import.AccountLoanPattern,
			CreditPattern:  cfg.Import.AccountCreditPattern,
			CashPattern:    cfg.Import.AccountCashPattern,
			SavingsPattern: cfg.Import.AccountSavingsPattern,
			DebitPattern:   cfg.Import.AccountDebitPattern,
		},
		RateFetchers: map[domain.RateSource]service.RateFetcher{
			// one request a second stays under the rate limit of the CBR site
			domain.RateSourceCBR: exchangerate.NewCBR(newHTTPClient(time.Minute), exchangerate.CBRURL, time.Second),
		},
		Banks: banks,
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	return svc, func() { _ = store.Close() }, nil
}

// newBankOptions returns the options of bank sync, the clients of the banks logging to
// log, without a key when cfg has no secret key, which disables it.
func newBankOptions(cfg config.Banks, log *slog.Logger) (service.BankOptions, error) {
	if cfg.SecretKey == "" {
		return service.BankOptions{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(cfg.SecretKey)
	if err != nil || len(key) != 32 {
		return service.BankOptions{}, errors.Error("BANKS_SECRET_KEY: must be 32 bytes in base64")
	}
	ru, err := newRussianHTTPClient(time.Minute)
	if err != nil {
		return service.BankOptions{}, err
	}
	return service.BankOptions{
		Clients: map[domain.Bank]service.BankClient{
			domain.BankTBank: tbank.New(ru, tbank.IDURL, tbank.APIURL, log),
		},
		Key:          key,
		SyncInterval: cfg.SyncInterval,
		SyncOverlap:  cfg.SyncOverlap,
	}, nil
}
