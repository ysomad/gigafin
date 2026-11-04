package password_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/password"
)

func TestPasswordHash(t *testing.T) {
	t.Parallel()

	hash, err := password.Hash("correct horse")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$"))

	ok, err := password.Check(hash, "correct horse")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = password.Check(hash, "wrong horse")
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = password.Check("$bcrypt$x", "x")
	require.Error(t, err)

	for _, params := range []string{"m=19456,t=0,p=1", "m=19456,t=2,p=0", "m=4294967295,t=2,p=1"} {
		bad := strings.Replace(hash, "m=19456,t=2,p=1", params, 1)
		require.NotEqual(t, hash, bad)
		_, err = password.Check(bad, "correct horse")
		require.Error(t, err, params)
	}
}

func TestCheckPolicy(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, password.CheckPolicy("short"), password.ErrTooShort)
	require.ErrorIs(t, password.CheckPolicy("пароль12345"), password.ErrTooShort, "counts characters, not bytes")
	require.ErrorIs(t, password.CheckPolicy(strings.Repeat("x", 129)), password.ErrTooLong)
	require.ErrorIs(t, password.CheckPolicy("bad utf8 \xff\xfe!!"), password.ErrNotUTF8)
	require.NoError(t, password.CheckPolicy("correct horse"), "spaces allowed, no composition rules")
	require.NoError(t, password.CheckPolicy("пароль123456"))
	require.NoError(t, password.CheckPolicy(strings.Repeat("x", 128)))
}
