package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/errors"
)

func TestCheckPasswordPolicy(t *testing.T) {
	t.Parallel()

	for pwd, format := range map[string]string{
		"short":                  "must be at least %d characters",
		"пароль12345":            "must be at least %d characters", // counts characters, not bytes
		strings.Repeat("x", 129): "must be at most %d characters",
		"bad utf8 \xff\xfe!!":    "must be valid UTF-8",
	} {
		err := CheckPasswordPolicy(pwd)
		require.ErrorIs(t, err, errors.ErrInvalid, pwd)
		e, ok := errors.AsType[Error](err)
		require.True(t, ok, pwd)
		require.Equal(t, format, e.Format, pwd)
	}
	require.NoError(t, CheckPasswordPolicy("correct horse"), "spaces allowed, no composition rules")
	require.NoError(t, CheckPasswordPolicy("пароль123456"))
	require.NoError(t, CheckPasswordPolicy(strings.Repeat("x", 128)))
}
