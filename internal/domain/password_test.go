package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckPasswordPolicy(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, CheckPasswordPolicy("short"), ErrPasswordTooShort)
	require.ErrorIs(t, CheckPasswordPolicy("пароль12345"), ErrPasswordTooShort, "counts characters, not bytes")
	require.ErrorIs(t, CheckPasswordPolicy(strings.Repeat("x", 129)), ErrPasswordTooLong)
	require.ErrorIs(t, CheckPasswordPolicy("bad utf8 \xff\xfe!!"), ErrPasswordNotUTF8)
	require.NoError(t, CheckPasswordPolicy("correct horse"), "spaces allowed, no composition rules")
	require.NoError(t, CheckPasswordPolicy("пароль123456"))
	require.NoError(t, CheckPasswordPolicy(strings.Repeat("x", 128)))
}
