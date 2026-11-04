package config_test

import (
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/config"
	"github.com/ysomad/gigafin/internal/domain"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.Default(), cfg)
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("DB_PATH", "x.db")
	t.Setenv("LOGGER_LEVEL", "debug")
	t.Setenv("LOGGER_ADD_SOURCE", "true")
	t.Setenv("HTTP_ADDR", ":9000")
	t.Setenv("HTTP_SECURE_COOKIES", "true")
	t.Setenv("HTTP_MAX_UPLOAD_BYTES", "1024")
	t.Setenv("HTTP_STATIC_CACHE_TTL", "0")
	t.Setenv("HTTP_API_DOCS", "true")
	t.Setenv("SESSION_TTL", "720h")
	t.Setenv("SESSION_IDLE_TTL", "48h")
	t.Setenv("IMPORT_ACCOUNT_LOAN_PATTERN", "loan")
	t.Setenv("IMPORT_ACCOUNT_CREDIT_PATTERN", `(?i)^platinum\b|card{1,2}`)
	t.Setenv("IMPORT_ACCOUNT_CASH_PATTERN", "cash")
	t.Setenv("IMPORT_ACCOUNT_SAVINGS_PATTERN", "savings")
	t.Setenv("IMPORT_ACCOUNT_DEBIT_PATTERN", "debit")
	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.Config{
		DBPath:  "x.db",
		Logger:  config.Logger{Level: slog.LevelDebug, AddSource: true},
		HTTP:    config.HTTP{Addr: ":9000", SecureCookies: true, MaxUploadBytes: 1024, APIDocs: true},
		Session: config.Session{TTL: 720 * time.Hour, IdleTTL: 48 * time.Hour},
		Import: config.Import{
			AccountLoanPattern:    "loan",
			AccountCreditPattern:  `(?i)^platinum\b|card{1,2}`, // commas survive
			AccountCashPattern:    "cash",
			AccountSavingsPattern: "savings",
			AccountDebitPattern:   "debit",
		},
	}, cfg)
}

func TestLoadPartialEnv(t *testing.T) {
	t.Setenv("SESSION_IDLE_TTL", "48h")
	cfg, err := config.Load()
	require.NoError(t, err)
	want := config.Default()
	want.Session.IdleTTL = 48 * time.Hour
	assert.Equal(t, want, cfg, "unset keeps the default")
}

func TestLoadInvalidEnv(t *testing.T) {
	for k, v := range map[string]string{
		"LOGGER_LEVEL":             "loud",
		"LOGGER_ADD_SOURCE":        "maybe",
		"HTTP_SECURE_COOKIES":   "maybe",
		"HTTP_MAX_UPLOAD_BYTES": "1MB",
		"SESSION_TTL":           "month",
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			_, err := config.Load()
			require.Error(t, err)
		})
	}
}

func TestDefaultAccountKinds(t *testing.T) {
	t.Parallel()

	imp := config.Default().Import
	// same order as service.newImports
	rules := make([]domain.AccountKindRule, 0, 5)
	for _, p := range []struct {
		kind    domain.AccountKind
		pattern string
	}{
		{kind: domain.AccountLoan, pattern: imp.AccountLoanPattern},
		{kind: domain.AccountCredit, pattern: imp.AccountCreditPattern},
		{kind: domain.AccountCash, pattern: imp.AccountCashPattern},
		{kind: domain.AccountSavings, pattern: imp.AccountSavingsPattern},
		{kind: domain.AccountDebit, pattern: imp.AccountDebitPattern},
	} {
		re, err := regexp.Compile(p.pattern)
		require.NoError(t, err)
		rules = append(rules, domain.AccountKindRule{Kind: p.kind, Pattern: re})
	}
	want := map[domain.AccountKind][]string{
		domain.AccountLoan: {
			"Ипотека", "Автокредит", "Кредит наличными", "Потребительский кредит", "Кредит",
			"Рефинансирование", "Кредит под залог авто", "Car loan", "Mortgage",
		},
		domain.AccountCredit: {
			"Кредитная карта", "Кредитка", "Tinkoff Platinum", "Кредитная СберКарта", "Халва",
			"Карта рассрочки", "Credit card",
		},
		domain.AccountCash: {"Наличные", "Кошелёк", "Cash", "Wallet"},
		domain.AccountSavings: {
			"Накопительный счёт", "Вклад", "Вклад (RUB)", "Депозит", "Сберегательный счёт", "Копилка",
			"Savings", "Deposit",
		},
		domain.AccountDebit: {
			"Tinkoff Black", "СберКарта", "Альфа-Карта", "Мультикарта ВТБ", "Зарплатная карта",
			"Debit card", "Loans4you", "Unknown",
		},
	}
	for kind, names := range want {
		for _, name := range names {
			assert.Equal(t, kind, domain.GuessAccountKind(name, rules), name)
		}
	}
}
