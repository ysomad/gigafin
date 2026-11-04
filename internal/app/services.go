package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/exchangerate"
	"github.com/ysomad/gigafin/internal/httpclient"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

// newService opens the database of cfg and returns the services over it, logging to log,
// with a func that closes the database.
func newService(ctx context.Context, cfg config.Config, log *slog.Logger) (*service.Services, func(), error) {
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
			domain.RateSourceCBR: exchangerate.NewCBR(httpclient.New(time.Minute), exchangerate.CBRURL, time.Second),
		},
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	return svc, func() { _ = store.Close() }, nil
}
