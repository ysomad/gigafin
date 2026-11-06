package domain

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ysomad/gigafin/internal/errors"
)

// SessionLifetime bounds how long a browser session lives.
type SessionLifetime struct {
	// Max is the longest a session lives, counted from sign-in.
	Max time.Duration
	// Idle ends a session that is not used for this long.
	Idle time.Duration
}

// Validate checks that both durations of l are positive.
func (l SessionLifetime) Validate() error {
	if l.Max <= 0 || l.Idle <= 0 {
		return NewError(errors.ErrInvalid, "session lifetime: must be positive")
	}
	return nil
}

// Session is a browser session.
type Session struct {
	ID         int64
	User       User
	UserAgent  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	// Current marks the session of the request that lists sessions.
	Current bool
}

// Live reports whether s has not expired at now.
func (s Session) Live(now time.Time) bool {
	return now.Before(s.ExpiresAt)
}

// ParseUserAgent trims the User-Agent header of a new session. It is shown in the
// session list only, so a long or missing header is cut or replaced, never an error.
func ParseUserAgent(s string) string {
	const maxLen = 200
	s = strings.TrimSpace(s)
	if len(s) > maxLen {
		s = strings.ToValidUTF8(s[:maxLen], "")
	}
	if s == "" {
		return "unknown"
	}
	return s
}

// Expiry returns when a session created at created and last used at now expires:
// after l.Idle of inactivity, at most l.Max after creation.
func (l SessionLifetime) Expiry(created, now time.Time) time.Time {
	idle, maxAge := now.Add(l.Idle), created.Add(l.Max)
	if idle.After(maxAge) {
		return maxAge
	}
	return idle
}

// Touch returns the expiry of s used at now under l and whether it should be stored.
func (s Session) Touch(now time.Time, l SessionLifetime) (time.Time, bool) {
	// how far the expiry must move before it is stored, so a request does not write
	// on every page view
	const refresh = time.Hour
	exp := l.Expiry(s.CreatedAt, now)
	return exp, exp.Sub(s.ExpiresAt) >= refresh
}

// Role grants a user a set of permissions.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// ParseRole validates a role string.
func ParseRole(s string) (Role, error) {
	switch r := Role(strings.TrimSpace(s)); r {
	case RoleUser, RoleAdmin:
		return r, nil
	default:
		return "", NewError(errors.ErrInvalid, "role: must be user or admin")
	}
}

// Permission is an action not every user may perform.
type Permission string

// PermBackup is downloading a backup of the whole database, data of every user and
// credential hashes included.
const PermBackup Permission = "backup"

// _rolePermissions are the permissions of each role beyond what every user may do.
var _rolePermissions = map[Role][]Permission{
	RoleAdmin: {PermBackup},
}

// User is an authenticated user.
type User struct {
	ID       int64
	Login    string
	Timezone string
	Role     Role
	loc      *time.Location
}

// NewUser returns a user with its time zone resolved; an invalid zone becomes UTC.
func NewUser(id int64, login, timezone string, role Role) User {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return User{ID: id, Login: login, Timezone: timezone, Role: role, loc: loc}
}

// Can reports whether u's role grants p.
func (u User) Can(p Permission) bool {
	return slices.Contains(_rolePermissions[u.Role], p)
}

// Location returns the user's time zone.
func (u User) Location() *time.Location {
	if u.loc == nil {
		return time.UTC
	}
	return u.loc
}

// ParseLogin trims and checks a login.
func ParseLogin(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", NewError(errors.ErrInvalid, "login: required")
	}
	if utf8.RuneCountInString(s) > MaxTextLen {
		return "", NewError(errors.ErrInvalid, "login: too long")
	}
	return s, nil
}

// ParseTimezone checks tz is a loadable IANA time zone.
func ParseTimezone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" || tz == "Local" {
		return "", NewError(errors.ErrInvalid, "time zone: unknown time zone %q", tz)
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", NewError(errors.ErrInvalid, "time zone: unknown time zone %q", tz)
	}
	return tz, nil
}

// Scope limits what an API key may do.
type Scope string

const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
)

// ParseScope validates a scope string.
func ParseScope(s string) (Scope, error) {
	switch sc := Scope(s); sc {
	case ScopeRead, ScopeWrite:
		return sc, nil
	default:
		return "", NewError(errors.ErrInvalid, "scope: must be read or write")
	}
}

// Allows reports whether a key with scope s may perform an action needing want.
func (s Scope) Allows(want Scope) bool {
	return s == ScopeWrite || s == want
}

// ParseKeyTTL parses the lifetime of a new API key: a number of days from a fixed set,
// or "never", which returns 0.
func ParseKeyTTL(s string) (time.Duration, error) {
	days, ok := map[string]int{"never": 0, "7": 7, "30": 30, "90": 90, "365": 365}[s]
	if !ok {
		return 0, NewError(errors.ErrInvalid, "expiration: must be 7, 30, 90, 365 days or never")
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// APIKey describes an API key without its secret. One key opens both the REST API and
// the MCP server.
type APIKey struct {
	ID     int64
	UserID int64
	Name   string
	Prefix string
	Scope  Scope
	// ExpiresAt is zero for a key that never expires.
	ExpiresAt time.Time
	// Expired marks a key past ExpiresAt when keys are listed.
	Expired    bool
	LastUsedAt time.Time
	Revoked    bool
	CreatedAt  time.Time
}

// Live reports whether k is usable at now: not revoked and not expired.
func (k APIKey) Live(now time.Time) bool {
	return !k.Revoked && !k.ExpiredAt(now)
}

// ExpiredAt reports whether k has an expiry that passed at now.
func (k APIKey) ExpiredAt(now time.Time) bool {
	return !k.ExpiresAt.IsZero() && !now.Before(k.ExpiresAt)
}

// Touch reports whether using k at now should be stored as its last use, so a
// request does not write on every call.
func (k APIKey) Touch(now time.Time) bool {
	// how stale the stored last use may get
	const refresh = time.Hour
	return now.Sub(k.LastUsedAt) >= refresh
}

// ParseAPIKeyName trims and checks the name of an API key.
func ParseAPIKeyName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", NewError(errors.ErrInvalid, "name: required")
	}
	if utf8.RuneCountInString(s) > MaxTextLen {
		return "", NewError(errors.ErrInvalid, "name: too long")
	}
	return s, nil
}
