// Package domain holds the models and business rules of gigafin: accounts,
// categories, operations that can be split across categories, plans, imports and users. It does no I/O;
// services load what a rule needs and store what it returns.
package domain

import "fmt"

// MaxTextLen limits payees, notes and names, in characters.
const MaxTextLen = 500

// MaxTags limits the tags of an operation.
const MaxTags = 10

// MaxTagLen limits a tag name, in characters.
const MaxTagLen = 50

// Error is a message meant for the user, kept as a format and its arguments so
// the UI can translate it. Args of type Msg are translated too, others, such as user
// input, are shown as is. Cause is either an error kind, such as errors.ErrInvalid,
// which ends the message, or another error, whose message follows. Field, if set, is
// the input the message is about; the UI shows the message next to it.
//
//	domain.NewError(errors.ErrInvalid, "name: too long")
//	domain.NewError(err, "line %d", line)
//	domain.NewError(errors.ErrConflict, "import is %s", domain.Msg(i.Status))
type Error struct {
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

// type check
var _ error = Error{}

// Error implements the error interface for Error.
func (err Error) Error() string {
	return fmt.Sprintf(err.Format, err.Args...) + ": " + err.Cause.Error()
}

// Unwrap returns the cause of err.
func (err Error) Unwrap() error {
	return err.Cause
}
