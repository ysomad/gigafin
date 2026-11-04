package credential_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/credential"
	"github.com/ysomad/gigafin/internal/errors"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("api key", func(t *testing.T) {
		t.Parallel()

		tok, err := credential.New(credential.KindAPIKey)
		require.NoError(t, err)
		assert.Equal(t, credential.KindAPIKey, tok.Kind)
		assert.True(t, strings.HasPrefix(tok.Token, "gf_"+tok.Prefix+"_"))
		assert.True(t, credential.Matches(tok.Token, tok.Hash))
		assert.False(t, credential.Matches(tok.Token+"x", tok.Hash))

		prefix, ok := credential.Parse(tok.Token)
		require.True(t, ok)
		assert.Equal(t, tok.Prefix, prefix)
	})

	t.Run("session", func(t *testing.T) {
		t.Parallel()

		tok, err := credential.New(credential.KindSession)
		require.NoError(t, err)
		assert.Equal(t, credential.KindSession, tok.Kind)
		assert.Empty(t, tok.Prefix)
		assert.Len(t, tok.Token, 64)
		assert.True(t, credential.Matches(tok.Token, tok.Hash))
		_, ok := credential.Parse(tok.Token)
		assert.False(t, ok)
	})

	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()

		_, err := credential.New("password")
		require.ErrorIs(t, err, errors.ErrInvalid)
	})
}

func TestParseMalformed(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"", "gf_", "gf_abc", "gf_abc_", "gf__secret", "xx_abc_secret"} {
		t.Run(token, func(t *testing.T) {
			t.Parallel()

			_, ok := credential.Parse(token)
			assert.False(t, ok)
		})
	}
}
