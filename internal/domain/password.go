package domain

import (
	"fmt"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
)

// Length limits in characters, OWASP ASVS 2.1: at least 12, at least 64 allowed, at
// most 128 so huge inputs are refused.
const (
	_pwdMinLen = 12
	_pwdMaxLen = 128
)

var (
	ErrPasswordTooShort = fmt.Errorf("password: must be at least %d characters: %w", _pwdMinLen, errors.ErrInvalid)
	ErrPasswordTooLong  = fmt.Errorf("password: must be at most %d characters: %w", _pwdMaxLen, errors.ErrInvalid)
	ErrPasswordNotUTF8  = fmt.Errorf("password: must be valid UTF-8: %w", errors.ErrInvalid)
)

func CheckPasswordPolicy(pwd string) error {
	if !utf8.ValidString(pwd) {
		return ErrPasswordNotUTF8
	}
	n := utf8.RuneCountInString(pwd)
	if n < _pwdMinLen {
		return ErrPasswordTooShort
	}
	if n > _pwdMaxLen {
		return ErrPasswordTooLong
	}
	return nil
}
