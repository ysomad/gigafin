package repotest_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/ysomad/gigafin/migrations"
)

func TestMigrationsUpDownUp(t *testing.T) {
	t.Parallel()

	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, raw.Close()) })

	p, err := goose.NewProvider(goose.DialectSQLite3, raw, migrations.FS)
	require.NoError(t, err)
	ctx := t.Context()
	_, err = p.Up(ctx)
	require.NoError(t, err)
	_, err = p.DownTo(ctx, 0)
	require.NoError(t, err)
	_, err = p.Up(ctx)
	require.NoError(t, err)
}
