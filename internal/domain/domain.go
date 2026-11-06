// Package domain holds the models and business rules of gigafin: accounts,
// categories, operations that can be split across categories, plans, imports and users. It does no I/O;
// services load what a rule needs and store what it returns.
package domain

import "fmt"

// _maxTextLen limits payees, notes and names, in characters.
const _maxTextLen = 500

// _maxTags limits the tags of an operation.
const _maxTags = 10

// Error is a message meant for the user, kept as a format and its arguments so
// the UI can translate it. Args of type Msg are translated too, others, such as user
// input, are shown as is. Cause is either an error kind, such as errors.ErrInvalid,
// which ends the message, or another error, whose message follows. Field, if set, is
// the input the message is about; the UI shows the message next to it.
//
//	domain.NewFieldError(errors.ErrInvalid, "name", "too long")
//	domain.NewError(err, "line %d", line)
//	domain.NewError(errors.ErrConflict, "import is %s", domain.Msg(i.Status))
type Error struct {
	Field  string
	Format string
	Args   []any
	Cause  error
}

// Msg is an argument of Error that is English text or an enum value, translated
// along with the format.
type Msg string

// NewError returns an Error of format and args with cause.
func NewError(cause error, format string, args ...any) error {
	return Error{Format: format, Args: args, Cause: cause}
}

// NewFieldError returns an Error about field of format and args with cause.
func NewFieldError(cause error, field, format string, args ...any) error {
	return Error{Field: field, Format: format, Args: args, Cause: cause}
}

// type check
var _ error = Error{}

// Error implements the error interface for Error.
func (err Error) Error() (msg string) {
	msg = fmt.Sprintf(err.Format, err.Args...) + ": " + err.Cause.Error()
	if err.Field != "" {
		msg = err.Field + ": " + msg
	}
	return msg
}

// Unwrap returns the cause of err.
func (err Error) Unwrap() error {
	return err.Cause
}
