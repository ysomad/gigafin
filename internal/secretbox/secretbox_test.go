package secretbox_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/secretbox"
)

func TestBox(t *testing.T) {
	t.Parallel()

	box, err := secretbox.New(make([]byte, 32))
	require.NoError(t, err)

	sealed, err := box.Seal("1", []byte("secret"))
	require.NoError(t, err)
	plain, err := box.Open("1", sealed)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(plain))

	_, err = box.Open("2", sealed)
	require.Error(t, err, "another aad")
	_, err = box.Open("1", sealed[:4])
	require.Error(t, err, "too short")

	other, err := secretbox.New(append(make([]byte, 31), 1))
	require.NoError(t, err)
	_, err = other.Open("1", sealed)
	require.Error(t, err, "another key")

	_, err = secretbox.New(make([]byte, 7))
	require.Error(t, err)
}
