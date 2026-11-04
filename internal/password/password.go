// Package password hashes and checks user passwords with argon2id.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/ysomad/gigafin/internal/errors"
)

// argon2id parameters, OWASP recommended minimum: 19 MiB, 2 iterations, 1 lane.
const (
	_argonTime    = 2
	_argonMemory  = 19 * 1024
	_argonThreads = 1
	_argonKeyLen  = 32
	_saltLen      = 16
)

// Length limits in characters, OWASP ASVS 2.1: at least 12, at least 64 allowed, at
// most 128 so huge inputs are refused.
const (
	_minLen = 12
	_maxLen = 128
)

const errBadHash errors.Error = "malformed password hash"

// Policy errors are errors.ErrInvalid errors of the field "password".
var (
	// ErrTooShort is returned for passwords shorter than the policy allows.
	ErrTooShort = fmt.Errorf("password: must be at least %d characters: %w", _minLen, errors.ErrInvalid)
	// ErrTooLong is returned for passwords longer than the policy allows.
	ErrTooLong = fmt.Errorf("password: must be at most %d characters: %w", _maxLen, errors.ErrInvalid)
	// ErrNotUTF8 is returned for passwords that are not valid UTF-8.
	ErrNotUTF8 = fmt.Errorf("password: must be valid UTF-8: %w", errors.ErrInvalid)
)

// _slots bounds concurrent argon2 runs, so a burst of logins cannot allocate
// _argonMemory per request without limit.
var _slots = make(chan struct{}, runtime.NumCPU())

// Hash returns an encoded argon2id hash of password.
func Hash(password string) (string, error) {
	salt := make([]byte, _saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := idKey(password, salt, _argonTime, _argonMemory, _argonThreads)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, _argonMemory, _argonTime, _argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Check reports whether password matches the encoded hash.
func Check(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var (
		memory, iterations uint32
		threads            uint8
	)
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false, errBadHash
	}
	// zero params panic in argon2 and a huge memory exhausts it; only own params are valid
	if memory != _argonMemory || iterations != _argonTime || threads != _argonThreads {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) != _argonKeyLen {
		return false, errBadHash
	}
	got := idKey(password, salt, iterations, memory, threads)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// CheckPolicy checks a new password. Length counts characters, not bytes. Following
// OWASP there are no composition rules: any characters, spaces included, are allowed
// and the password is never truncated.
func CheckPolicy(password string) error {
	if !utf8.ValidString(password) {
		return ErrNotUTF8
	}
	n := utf8.RuneCountInString(password)
	if n < _minLen {
		return ErrTooShort
	}
	if n > _maxLen {
		return ErrTooLong
	}
	return nil
}

func idKey(password string, salt []byte, iterations, memory uint32, threads uint8) []byte {
	_slots <- struct{}{}
	defer func() { <-_slots }()
	return argon2.IDKey([]byte(password), salt, iterations, memory, threads, _argonKeyLen)
}
