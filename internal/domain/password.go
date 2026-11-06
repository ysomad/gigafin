package domain

import (
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
)

// Length limits in characters, OWASP ASVS 2.1: at least 12, at least 64 allowed, at
// most 128 so huge inputs are refused.
const (
	_pwdMinLen = 12
	_pwdMaxLen = 128
)

// CheckPasswordPolicy returns an error about the password field if pwd is not valid
// UTF-8, too short or too long.
func CheckPasswordPolicy(pwd string) error {
	if !utf8.ValidString(pwd) {
		return NewFieldError(errors.ErrInvalid, "password", "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(pwd)
	if n < _pwdMinLen {
		return NewFieldError(errors.ErrInvalid, "password", "must be at least %d characters", _pwdMinLen)
	}
	if n > _pwdMaxLen {
		return NewFieldError(errors.ErrInvalid, "password", "must be at most %d characters", _pwdMaxLen)
	}
	return nil
}
