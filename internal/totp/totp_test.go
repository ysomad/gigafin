package totp_test

import (
	"encoding/base32"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/totp"
)

// _rfcSecret is the SHA1 key of the RFC 6238 test vectors, "12345678901234567890".
var _rfcSecret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))

func TestCode(t *testing.T) {
	t.Parallel()

	// RFC 6238 appendix B, the last 6 of its 8 digits
	tests := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, tt := range tests {
		got, err := totp.Code(_rfcSecret, totp.Step(time.Unix(tt.unix, 0)))
		require.NoError(t, err)
		assert.Equal(t, tt.want, got, tt.unix)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()

	now := time.Unix(1111111111, 0)
	cur := totp.Step(now)

	step, ok := totp.Verify(_rfcSecret, "050471", now, 0)
	require.True(t, ok)
	assert.Equal(t, cur, step)
	_, ok = totp.Verify(_rfcSecret, " 050 471 ", now, 0)
	assert.True(t, ok, "spaces are ignored")

	_, ok = totp.Verify(_rfcSecret, "050471", now, cur)
	assert.False(t, ok, "a used code does not work again")

	prev, err := totp.Code(_rfcSecret, cur-1)
	require.NoError(t, err)
	step, ok = totp.Verify(_rfcSecret, prev, now, 0)
	require.True(t, ok, "the previous step is accepted")
	assert.Equal(t, cur-1, step)

	old, err := totp.Code(_rfcSecret, cur-2)
	require.NoError(t, err)
	_, ok = totp.Verify(_rfcSecret, old, now, 0)
	assert.False(t, ok, "two steps ago is too old")

	for _, code := range []string{"", "05047", "0504710", "000000"} {
		_, ok = totp.Verify(_rfcSecret, code, now, 0)
		assert.False(t, ok, code)
	}
}

func TestSecret(t *testing.T) {
	t.Parallel()

	s, err := totp.NewSecret()
	require.NoError(t, err)
	assert.Len(t, s, 32)
	assert.True(t, totp.ValidSecret(s))
	assert.False(t, totp.ValidSecret(s[:16]))
	assert.False(t, totp.ValidSecret("not base32!"))

	u, err := url.Parse(totp.URI("gigafin", "alice", s))
	require.NoError(t, err)
	assert.Equal(t, "otpauth", u.Scheme)
	assert.Equal(t, "totp", u.Host)
	assert.Equal(t, "/gigafin:alice", u.Path)
	assert.Equal(t, s, u.Query().Get("secret"))
	assert.Equal(t, "gigafin", u.Query().Get("issuer"))

	u, err = url.Parse(totp.URI("gigafin", "alice:work", s))
	require.NoError(t, err)
	assert.Equal(t, "/gigafin:alice_work", u.Path, "one colon separates issuer and account")
}
