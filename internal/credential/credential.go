// Package credential generates session, API key and recovery code tokens and the hashes
// stored for them.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/password"
)

// Kind is the kind of a credential.
type Kind string

const (
	KindSession  Kind = "session"
	KindAPIKey   Kind = "api"
	KindRecovery Kind = "recovery"
)

// _apiKeyTag marks API key tokens, so Parse tells them from session tokens without a lookup.
const _apiKeyTag = "gf_"

// Token is a freshly generated secret with the hash to store.
type Token struct {
	Kind  Kind
	Token string
	Hash  string
	// Prefix identifies a key token or recovery code; empty for session tokens.
	Prefix string
}

// _recoveryAlphabet writes recovery codes in lower-case base32, which has no characters
// that look alike.
const _recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

var _recoveryEncoding = base32.NewEncoding(_recoveryAlphabet).WithPadding(base32.NoPadding)

const (
	// _recoveryLen is the length of a recovery code without dashes, 60 bits.
	_recoveryLen = 12
	// _recoveryPrefixLen is the length of the prefix a recovery code is looked up by.
	_recoveryPrefixLen = 4
)

// New generates a token of kind. Session tokens are untagged random hex, API key tokens
// have the form gf_<prefix>_<secret>. Recovery codes have the form xxxx-xxxx-xxxx, the
// first group their prefix; their hash is an argon2id hash, see ParseRecoveryCode and
// password.Compare, as they are too short for a plain hash.
func New(kind Kind) (Token, error) {
	if kind == KindRecovery {
		b := make([]byte, 8 /* bytes, 13 base32 characters */)
		if _, err := rand.Read(b); err != nil {
			return Token{}, fmt.Errorf("read random: %w", err)
		}
		code := _recoveryEncoding.EncodeToString(b)[:_recoveryLen]
		hash, err := password.Hash(code)
		if err != nil {
			return Token{}, err
		}
		return Token{
			Kind: kind, Token: code[:4] + "-" + code[4:8] + "-" + code[8:], Hash: hash, Prefix: code[:_recoveryPrefixLen],
		}, nil
	}
	if kind == KindSession {
		token, err := randomHex(32 /* bytes */)
		if err != nil {
			return Token{}, err
		}
		return Token{Kind: kind, Token: token, Hash: Hash(token)}, nil
	}
	if kind != KindAPIKey {
		return Token{}, fmt.Errorf("credential kind %q: %w", kind, errors.ErrInvalid)
	}
	prefix, err := randomHex(6 /* bytes */)
	if err != nil {
		return Token{}, err
	}
	secret, err := randomHex(24 /* bytes */)
	if err != nil {
		return Token{}, err
	}
	token := _apiKeyTag + prefix + "_" + secret
	return Token{Kind: kind, Token: token, Hash: Hash(token), Prefix: prefix}, nil
}

// Parse returns the prefix of an API key token. Session tokens are not parsed, they are
// looked up by hash.
func Parse(token string) (prefix string, ok bool) {
	rest, ok := strings.CutPrefix(token, _apiKeyTag)
	if !ok {
		return "", false
	}
	prefix, secret, ok := strings.Cut(rest, "_")
	if !ok || prefix == "" || secret == "" {
		return "", false
	}
	return prefix, true
}

// ParseRecoveryCode returns the prefix of a typed recovery code and the form of it that
// is hashed: lower case, without spaces and dashes.
func ParseRecoveryCode(s string) (prefix, code string, ok bool) {
	code = strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, strings.ToLower(s))
	if len(code) != _recoveryLen || strings.Trim(code, _recoveryAlphabet) != "" {
		return "", "", false
	}
	return code[:_recoveryPrefixLen], code, true
}

// Hash returns the stored form of a token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Matches compares a token with a stored hash in constant time.
func Matches(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(Hash(token)), []byte(hash)) == 1
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
