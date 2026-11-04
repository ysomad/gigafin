package config

import (
	"log/slog"
	"time"
)

// Default returns the settings used when the environment sets nothing.
//
// Account patterns cover product names of Russian banks (T-Bank, Sber, Alfa, VTB,
// Sovcombank and others) and generic English names. RE2 has no lookahead and \b is
// ASCII-only, so Cyrillic words are matched by stem.
func Default() Config {
	return Config{
		DBPath: "gigafin.db",
		Logger: Logger{Level: slog.LevelInfo},
		HTTP: HTTP{
			Addr:           "127.0.0.1:8080",
			MaxUploadBytes: 10 << 20,
			StaticCacheTTL: time.Hour,
		},
		Session: Session{
			TTL:     30 * 24 * time.Hour,
			IdleTTL: 7 * 24 * time.Hour,
		},
		Import: Import{ //nolint:gosec // G101 false positive: account name patterns, not credentials
			// Bare "кредит" not followed by н or к, so "Кредитная" and "Кредитка" stay cards.
			AccountLoanPattern: `(?i)ипотек|автокредит|кредит($|[^нк])|займ|заём|рефинанс|залог|` +
				`\bloan\b|mortgage`,
			AccountCreditPattern:  `(?i)кредитн|кредитк|рассрочк|халва|совесть|platinum|платинум|\bcredit\b`,
			AccountCashPattern:    `(?i)налич|кошел[её]к|\bcash\b|wallet`,
			AccountSavingsPattern: `(?i)накопит|сберегат|вклад|депозит|копилк|savings|deposit`,
			AccountDebitPattern: `(?i)дебет|зарплат|пенсион|детск|` +
				`платёжн|платежн|расч[её]тн|текущ|сберкарт|мультикарт|альфа-карт|black|junior|` +
				`\bdebit\b|checking`,
		},
	}
}
