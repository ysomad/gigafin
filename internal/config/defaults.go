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
			// Bare "кредит" not followed by н or к, so "Кредитная" and "Кредитка" stay cards;
			// "долг" not followed by о, so "Долгосрочный вклад" stays savings.
			AccountLoanPattern: `(?i)ипотек|автокредит|кредит($|[^нк])|займ|заём|рефинанс|залог|долг($|[^о])|` +
				`\bloan\b|\bdebts?\b|mortgage`,
			AccountCreditPattern:  `(?i)кредитн|кредитк|рассрочк|халва|совесть|platinum|платинум|\bcredit\b`,
			AccountCashPattern:    `(?i)налич|кошел[её]к|\bcash\b|wallet`,
			AccountSavingsPattern: `(?i)накопит|сберегат|вклад|депозит|копилк|savings|deposit`,
			AccountDebitPattern: `(?i)дебет|зарплат|пенсион|детск|` +
				`платёжн|платежн|расч[её]тн|текущ|сберкарт|мультикарт|альфа-карт|black|junior|` +
				`\bdebit\b|checking`,
		},
		Banks: Banks{
			SyncInterval: 15 * time.Minute,
			// holds of hotels and car rentals settle weeks later, dated when they were made;
			// operations synced before are left out, so a long overlap costs only the fetch
			SyncOverlap: 45 * 24 * time.Hour,
		},
	}
}
