package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
)

func TestScopeAllows(t *testing.T) {
	t.Parallel()

	assert.True(t, domain.ScopeRead.Allows(domain.ScopeRead))
	assert.False(t, domain.ScopeRead.Allows(domain.ScopeWrite))
	assert.True(t, domain.ScopeWrite.Allows(domain.ScopeRead))
	assert.True(t, domain.ScopeWrite.Allows(domain.ScopeWrite))
}

func TestSessionTouch(t *testing.T) {
	t.Parallel()

	life := domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour}
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s := domain.Session{CreatedAt: created, ExpiresAt: life.Expiry(created, created)}
	assert.Equal(t, created.Add(life.Idle), s.ExpiresAt, "idle expiry from sign-in")

	_, ok := s.Touch(created.Add(time.Minute), life)
	assert.False(t, ok, "not stored on every request")

	exp, ok := s.Touch(created.Add(2*time.Hour), life)
	assert.True(t, ok)
	want := created.Add(2*time.Hour + life.Idle)
	assert.Equal(t, want, exp)

	exp, _ = s.Touch(created.Add(life.Max-time.Hour), life)
	want = created.Add(life.Max)
	assert.Equal(t, want, exp, "capped at the absolute lifetime")
}

func TestSessionLifetimeValidate(t *testing.T) {
	t.Parallel()

	require.NoError(t, domain.SessionLifetime{Max: time.Hour, Idle: time.Minute}.Validate())
	require.ErrorIs(t, domain.SessionLifetime{Max: time.Hour}.Validate(), errors.ErrInvalid, "no idle")
	require.ErrorIs(t, domain.SessionLifetime{Idle: time.Hour}.Validate(), errors.ErrInvalid, "no max")
}

func TestParseAPIKeyName(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseAPIKeyName("  laptop ")
	require.NoError(t, err)
	assert.Equal(t, "laptop", got)

	_, err = domain.ParseAPIKeyName(" ")
	require.ErrorContains(t, err, "name: required")

	_, err = domain.ParseAPIKeyName(strings.Repeat("a", 501))
	require.ErrorContains(t, err, "name: too long")
}

func TestSessionLive(t *testing.T) {
	t.Parallel()

	exp := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s := domain.Session{ExpiresAt: exp}
	assert.True(t, s.Live(exp.Add(-time.Second)))
	assert.False(t, s.Live(exp), "expired at the expiry instant")
	assert.False(t, s.Live(exp.Add(time.Second)))
}

func TestParseKeyTTL(t *testing.T) {
	t.Parallel()

	got, err := domain.ParseKeyTTL("30")
	require.NoError(t, err)
	assert.Equal(t, 30*24*time.Hour, got)

	got, err = domain.ParseKeyTTL("never")
	require.NoError(t, err)
	assert.Zero(t, got)

	for _, bad := range []string{"", "0", "14", "-7", "forever"} {
		_, err = domain.ParseKeyTTL(bad)
		require.ErrorContains(t, err, "expiration:", bad)
	}
}

func TestAPIKeyLive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	assert.True(t, domain.APIKey{}.Live(now), "never expires")
	assert.True(t, domain.APIKey{ExpiresAt: now.Add(time.Second)}.Live(now))
	assert.False(t, domain.APIKey{ExpiresAt: now}.Live(now), "expired")
	assert.False(t, domain.APIKey{Revoked: true}.Live(now), "revoked")
}

func TestAPIKeyTouch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	assert.True(t, domain.APIKey{}.Touch(now), "never used")
	assert.False(t, domain.APIKey{LastUsedAt: now.Add(-time.Minute)}.Touch(now))
	assert.True(t, domain.APIKey{LastUsedAt: now.Add(-time.Hour)}.Touch(now))
}

func TestRoleCan(t *testing.T) {
	t.Parallel()

	_, err := domain.ParseRole("root")
	require.ErrorIs(t, err, errors.ErrInvalid)
	_, err = domain.ParseRole("")
	require.ErrorIs(t, err, errors.ErrInvalid)
	admin, err := domain.ParseRole(" admin ")
	require.NoError(t, err)

	assert.True(t, domain.NewUser(1, "a", "UTC", admin).Can(domain.PermBackup))
	assert.False(t, domain.NewUser(1, "a", "UTC", domain.RoleUser).Can(domain.PermBackup))
	assert.False(t, domain.User{}.Can(domain.PermBackup), "no role, no permissions")
}
