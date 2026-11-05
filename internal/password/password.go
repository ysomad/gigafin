package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"

	"github.com/ysomad/gigafin/internal/errors"
	"golang.org/x/crypto/argon2"
)

// argon2id parameters, OWASP recommended minimum: 19 MiB, 2 iterations, 1 lane.
const (
	_argonTime    = 2
	_argonMemory  = 19 * 1024
	_argonThreads = 1
	_argonKeyLen  = 32
	_saltLen      = 16
)

const errBadHash errors.Error = "malformed password hash"

// _slots bounds concurrent argon2 runs, so a burst of logins cannot allocate
// _argonMemory per request without limit.
var _slots = make(chan struct{}, runtime.NumCPU())

// Hash derives an Argon2id hash with a random salt.
func Hash(pwd string) (string, error) {
	salt := make([]byte, _saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read salt: %w", err)
	}
	key := idKey(pwd, salt, _argonTime, _argonMemory, _argonThreads)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		_argonMemory,
		_argonTime,
		_argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Compare reports whether pwd matches an encoded Argon2id hash.
func Compare(hash, pwd string) (bool, error) {
	parts := strings.Split(hash, "$")
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
	if memory != _argonMemory || iterations != _argonTime || threads != _argonThreads {
		return false, errBadHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != _argonKeyLen {
		return false, errBadHash
	}

	got := idKey(pwd, salt, iterations, memory, threads)

	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func idKey(password string, salt []byte, iterations, memory uint32, threads uint8) []byte {
	_slots <- struct{}{}
	defer func() { <-_slots }()
	return argon2.IDKey([]byte(password), salt, iterations, memory, threads, _argonKeyLen)
}
