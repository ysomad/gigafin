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

// CheckPasswordPolicy returns an error if pwd is not valid
// UTF-8, too short or too long.
func CheckPasswordPolicy(pwd string) error {
	if !utf8.ValidString(pwd) {
		return NewError(errors.ErrInvalid, "must be valid UTF-8")
	}
	n := utf8.RuneCountInString(pwd)
	if n < _pwdMinLen {
		return NewError(errors.ErrInvalid, "must be at least %d characters", _pwdMinLen)
	}
	if n > _pwdMaxLen {
		return NewError(errors.ErrInvalid, "must be at most %d characters", _pwdMaxLen)
	}
	return nil
}
