// Package errors is a drop-in replacement and extension of the Go standard
// library's package [errors], copied from github.com/AdguardTeam/golibs/errors
// (public domain) with what gigafin does not use removed, and the error kinds of
// gigafin added.
package errors

import (
	stderrors "errors"
)

// NOTE:  Please keep only the stdlib-related identifiers in this file.

// Join returns an error that wraps the given errors.  Any nil error values are
// discarded.  Join returns nil if errs contains no non-nil values.  The error
// formats as the concatenation of the strings obtained by calling the Error
// method of each element of errs, with a newline between each string.
//
// It calls [stderrors.Join] from the Go standard library.
func Join(errs ...error) error {
	return stderrors.Join(errs...)
}

// As finds the first error in err's chain that matches target, and if so, sets
// target to that error value and returns true.  Otherwise, it returns false.
//
// It calls [stderrors.As] from the Go standard library.
func As(err error, target any) (ok bool) {
	return stderrors.As(err, target)
}

// AsType finds the first error in err's tree that matches the type E, and if
// one is found, returns that error value and true.  Otherwise, it returns the
// zero value of E and false.
//
// It calls [stderrors.AsType] from the Go standard library.
func AsType[E error](err error) (found E, ok bool) {
	return stderrors.AsType[E](err)
}

// Is reports whether any error in err's chain matches target.
//
// It calls [stderrors.Is] from the Go standard library.
func Is(err, target error) (ok bool) {
	return stderrors.Is(err, target)
}
