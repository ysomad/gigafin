package password

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHash(t *testing.T) {
	t.Parallel()

	hash, err := Hash("correct horse")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$"))

	ok, err := Compare(hash, "correct horse")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = Compare(hash, "wrong horse")
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = Compare("$bcrypt$x", "x")
	require.Error(t, err)

	for _, params := range []string{"m=19456,t=0,p=1", "m=19456,t=2,p=0", "m=4294967295,t=2,p=1"} {
		bad := strings.Replace(hash, "m=19456,t=2,p=1", params, 1)
		require.NotEqual(t, hash, bad)
		_, err = Compare(bad, "correct horse")
		require.Error(t, err, params)
	}
}
