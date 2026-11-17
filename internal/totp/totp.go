// Package totp generates and checks time-based one-time passwords (RFC 6238) the way
// authenticator apps do: HMAC-SHA1, 6 digits, 30 second steps.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 and every authenticator app use HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// Digits is the length of a code.
	Digits = 6
	// Period is how long a code is valid.
	Period = 30 * time.Second
	// _secretLen is the secret length in bytes, 160 bits as RFC 4226 recommends.
	_secretLen = 20
	// _skew is how many steps before and after now a code is accepted, for clocks that
	// drift and codes typed at the end of their step.
	_skew = 1
)

// _encoding is how secrets are shown and stored: upper-case base32 without padding.
var _encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a random secret in base32.
func NewSecret() (string, error) {
	b := make([]byte, _secretLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return _encoding.EncodeToString(b), nil
}

// ValidSecret reports whether secret is a base32 secret of NewSecret.
func ValidSecret(secret string) bool {
	b, err := _encoding.DecodeString(secret)
	return err == nil && len(b) == _secretLen
}

// URI returns the otpauth URI of secret that authenticator apps read from a QR code. Its
// label is issuer:account, so a colon in either is replaced, as apps split at the first.
func URI(issuer, account, secret string) string {
	issuer, account = strings.ReplaceAll(issuer, ":", "_"), strings.ReplaceAll(account, ":", "_")
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(Digits))
	q.Set("period", strconv.Itoa(int(Period.Seconds())))
	u := url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + issuer + ":" + account, RawQuery: q.Encode()}
	return u.String()
}

// Step returns the step of t.
func Step(t time.Time) int64 {
	return t.Unix() / int64(Period.Seconds())
}

// Code returns the code of secret at step.
func Code(secret string, step int64) (string, error) {
	key, err := _encoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("decode totp secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // steps are positive
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	// dynamic truncation, RFC 4226 5.3
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", Digits, n%1_000_000), nil
}

// Verify returns the step of the code of secret that matches code within _skew steps
// of now. Spaces in code are ignored, as apps show codes grouped. A step not after last
// does not match, so a code works only once; last is 0 when no code was used yet.
func Verify(secret, code string, now time.Time, last int64) (int64, bool) {
	code = strings.Join(strings.Fields(code), "")
	if len(code) != Digits {
		return 0, false
	}
	cur := Step(now)
	for step := cur - _skew; step <= cur+_skew; step++ {
		if step <= last {
			continue
		}
		want, err := Code(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
