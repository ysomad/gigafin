// Package repository stores domain models in SQLite. It is the only package that
// knows the schema and the generated sqlc code; everything it takes and returns is a
// domain type, with amounts converted between decimals and minor units.
package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

const _pragmas = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
	"&_pragma=synchronous(NORMAL)"

// Repos are all repositories over one connection or transaction.
type Repos struct {
	Users      *Users
	Settings   *Settings
	Sessions   *Sessions
	APIKeys    *APIKeys
	Accounts   *Accounts
	Categories *Categories
	Operations *Operations
	Plans      *Plans
	Imports    *Imports
	Rates      *Rates
	Rules      *Rules
}

// Store holds one write connection and a pool of read connections, since SQLite allows
// a single writer at a time.
type Store struct {
	w *sql.DB
	r *sql.DB
}

// Open opens the database at path. Migrations are applied separately with goose.
func Open(ctx context.Context, path string) (*Store, error) {
	w, err := open(ctx, path, "&_txlock=immediate", 1 /* conns */)
	if err != nil {
		return nil, err
	}
	r, err := open(ctx, path, "" /* extra */, 4 /* conns */)
	if err != nil {
		return nil, errors.Join(err, w.Close())
	}
	return &Store{w: w, r: r}, nil
}

// Close closes both pools.
func (s *Store) Close() error {
	return errors.Join(s.w.Close(), s.r.Close())
}

// Read returns repositories over the read pool.
func (s *Store) Read() Repos {
	return newRepos(sqlc.New(s.r))
}

// Write returns repositories over the write connection, outside a transaction.
func (s *Store) Write() Repos {
	return newRepos(sqlc.New(s.w))
}

// InTx runs fn in a write transaction and commits it if fn returns nil.
func (s *Store) InTx(ctx context.Context, fn func(r Repos) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// rolls back when fn fails or panics; after a commit, or when a canceled context
	// already rolled back, Rollback returns sql.ErrTxDone
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = errors.Join(err, rbErr)
		}
	}()
	if err := fn(newRepos(sqlc.New(tx))); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Backup writes a consistent copy of the whole database to path, which must not exist.
func (s *Store) Backup(ctx context.Context, path string) error {
	if _, err := s.r.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

func newRepos(q *sqlc.Queries) Repos {
	return Repos{
		Users:      &Users{q: q},
		Settings:   &Settings{q: q},
		Sessions:   &Sessions{q: q},
		APIKeys:    &APIKeys{q: q},
		Accounts:   &Accounts{q: q},
		Categories: &Categories{q: q},
		Operations: &Operations{q: q},
		Plans:      &Plans{q: q},
		Imports:    &Imports{q: q},
		Rates:      &Rates{q: q},
		Rules:      &Rules{q: q},
	}
}

func open(ctx context.Context, path, extra string, conns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?"+_pragmas+extra)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("ping sqlite: %w", err), db.Close())
	}
	return db, nil
}
