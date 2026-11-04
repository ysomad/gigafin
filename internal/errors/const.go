package errors

// Error is the constant error type.
//
// See https://dave.cheney.net/2016/04/07/constant-errors.
type Error string

// type check
var _ error = Error("")

// Error implements the error interface for Error.
func (err Error) Error() (msg string) {
	return string(err)
}

// Kinds classify errors of every layer, so handlers can answer them without knowing
// which layer failed. Wrap a kind with %w at the end of the message, so the message
// reads first:
//
//	fmt.Errorf("name: required: %w", errors.ErrInvalid)
//
// and match it with [Is]. An error of no kind is internal.
const (
	// ErrInvalid is bad input; the message is meant for the user.
	ErrInvalid Error = "invalid"
	// ErrNotFound is a missing row or route.
	ErrNotFound Error = "not found"
	// ErrExists is a duplicate of a unique value.
	ErrExists Error = "already exists"
	// ErrConflict is a request the current state does not allow: in use, wrong
	// status, unmapped import.
	ErrConflict Error = "conflict"
	// ErrUnauthorized is a missing session or key, or wrong credentials.
	ErrUnauthorized Error = "unauthorized"
	// ErrForbidden is an authenticated request that is not allowed.
	ErrForbidden Error = "forbidden"
)
