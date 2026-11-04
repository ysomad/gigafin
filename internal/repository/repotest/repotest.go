// Package repotest creates migrated SQLite databases for tests.
package repotest

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/repository"
)

// New returns a database in a temp dir with all migrations applied.
func New(t testing.TB) *repository.Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "gigafin.db")

	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()
	p, err := goose.NewProvider(goose.DialectSQLite3, raw, os.DirFS(MigrationsDir(t)))
	require.NoError(t, err)
	_, err = p.Up(t.Context())
	require.NoError(t, err)

	d, err := repository.Open(t.Context(), path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })
	return d
}

// MigrationsDir returns the absolute path of the migrations directory.
func MigrationsDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
}
