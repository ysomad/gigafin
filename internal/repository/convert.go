package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/errors"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// notFound maps a missing row to errors.ErrNotFound.
func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", what, errors.ErrNotFound)
	}
	return fmt.Errorf("get %s: %w", what, err)
}

// affected maps an update or delete that touched no rows to errors.ErrNotFound and a
// constraint failure to its kind.
func affected(n int64, err error, what string) error {
	if k, ok := constraint(err); ok {
		return fmt.Errorf("%s: %w", what, k)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, errors.ErrNotFound)
	}
	return nil
}

// constraint returns the kind of a constraint failure: errors.ErrExists for UNIQUE and
// PRIMARY KEY, errors.ErrConflict for FOREIGN KEY, as a row is in use or refers to a
// missing one. ok is false for other errors.
func constraint(err error) (errors.Error, bool) {
	serr, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return "", false
	}
	switch serr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return errors.ErrExists, true
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return errors.ErrConflict, true
	default:
		return "", false
	}
}

func nullUnix(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}
