// Package config reads settings from environment variables over the defaults in
// Default. A variable is named after its field in SCREAMING_SNAKE_CASE, such as
// DB_PATH for DBPath; a field of a nested struct is prefixed with the struct name,
// such as HTTP_ADDR for HTTP.Addr. It only holds values; the packages that use them
// interpret them.
package config

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is all settings of the app.
type Config struct {
	// DBPath is the SQLite database file, migrated with goose.
	DBPath  string
	Logger  Logger  `envPrefix:"LOGGER_"`
	HTTP    HTTP    `envPrefix:"HTTP_"`
	Session Session `envPrefix:"SESSION_"`
	Import  Import  `envPrefix:"IMPORT_"`
}

// Logger configures the JSON logs.
type Logger struct {
	// Level is debug, info, warn or error.
	Level slog.Level
	// AddSource adds the source file and line to every record.
	AddSource bool
}

// HTTP configures the server.
type HTTP struct {
	// Addr is the listen address, HOST:PORT.
	Addr string
	// SecureCookies is set when served over HTTPS: Secure __Host- cookies and HSTS.
	SecureCookies bool
	// MaxUploadBytes limits the body of a signed-in web request, an import file included.
	MaxUploadBytes int64
	// StaticCacheTTL is how long browsers cache static assets; 0 disables caching, for
	// development.
	StaticCacheTTL time.Duration
	// APIDocs serves the API docs at /api/docs; the binary must be built with the
	// apidocs tag.
	APIDocs bool
}

// Session configures browser sessions.
type Session struct {
	// TTL is the longest a session lives, counted from sign-in.
	TTL time.Duration
	// IdleTTL ends a session that is not used for this long.
	IdleTTL time.Duration
}

// Import configures bank imports.
type Import struct {
	// Regular expressions (Go RE2 syntax) matched against bank account names to pick
	// the kind of a new account. They are tried in the order loan, credit, cash,
	// savings, debit and the first match wins; no match is debit. Empty disables the kind.
	AccountLoanPattern    string
	AccountCreditPattern  string
	AccountCashPattern    string
	AccountSavingsPattern string
	AccountDebitPattern   string
}

// Load returns Default with values set in the environment replacing it.
func Load() (Config, error) {
	cfg := Default()
	if err := env.ParseWithOptions(&cfg, env.Options{UseFieldNameByDefault: true}); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}
