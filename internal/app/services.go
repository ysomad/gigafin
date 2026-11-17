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
	tb, err := newTBankOptions(cfg.TBank, log)
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
		TBank: tb,
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	return svc, func() { _ = store.Close() }, nil
}

// newTBankOptions returns the options of T-Bank sync, its client logging to log,
// without a client when cfg has no secret key, which disables it.
func newTBankOptions(cfg config.TBank, log *slog.Logger) (service.TBankOptions, error) {
	if cfg.SecretKey == "" {
		return service.TBankOptions{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(cfg.SecretKey)
	if err != nil || len(key) != 32 {
		return service.TBankOptions{}, errors.Error("TBANK_SECRET_KEY: must be 32 bytes in base64")
	}
	hc, err := newRussianHTTPClient(time.Minute)
	if err != nil {
		return service.TBankOptions{}, err
	}
	return service.TBankOptions{
		Client:       tbank.New(hc, tbank.IDURL, tbank.APIURL, log),
		Key:          key,
		SyncInterval: cfg.SyncInterval,
		SyncOverlap:  cfg.SyncOverlap,
	}, nil
}
