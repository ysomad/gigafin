// Package credential generates session and API key tokens and the hashes stored for them.
package credential

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ysomad/gigafin/internal/errors"
)

// Kind is the kind of a credential.
type Kind string

const (
	KindSession Kind = "session"
	KindAPIKey  Kind = "api"
)

// _apiKeyTag marks API key tokens, so Parse tells them from session tokens without a lookup.
const _apiKeyTag = "gf_"

// Token is a freshly generated secret with the hash to store.
type Token struct {
	Kind  Kind
	Token string
	Hash  string
	// Prefix identifies a key token; empty for session tokens.
	Prefix string
}

// New generates a token of kind. Session tokens are untagged random hex, API key tokens
// have the form gf_<prefix>_<secret>.
func New(kind Kind) (Token, error) {
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
