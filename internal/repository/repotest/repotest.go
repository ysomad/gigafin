// Package repotest creates migrated SQLite databases for tests.
package repotest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/repository"
)

// New returns a database in a temp dir with all migrations applied.
func New(t testing.TB) *repository.Store {
	t.Helper()

	d, err := repository.Open(t.Context(), filepath.Join(t.TempDir(), "gigafin.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, d.Close()) })
	return d
}
