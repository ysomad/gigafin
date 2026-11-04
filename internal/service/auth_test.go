package service_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

func TestLoginSessions(t *testing.T) {
	t.Parallel()

	svc := newServices(t, repotest.New(t)).Auth
	ctx := t.Context()

	_, err := svc.CreateUser(ctx, "alice", "short", "UTC", "user")
	require.ErrorIs(t, err, errors.ErrInvalid)
	_, err = svc.CreateUser(ctx, "alice", "password1234", "Mars/Olympus", "user")
	require.ErrorIs(t, err, errors.ErrInvalid)

	u, err := svc.CreateUser(ctx, "alice", "password1234", "Europe/Moscow", "user")
	require.NoError(t, err)
	_, err = svc.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.ErrorIs(t, err, errors.ErrExists)

	_, _, err = svc.Login(ctx, "alice", "password5678", "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	_, _, err = svc.Login(ctx, "bob", "password1234", "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)

	require.NoError(t, svc.CheckPassword(ctx, u.ID, "password1234"))
	require.ErrorIs(t, svc.CheckPassword(ctx, u.ID, "password5678"), errors.ErrUnauthorized)

	token, got, err := svc.Login(ctx, "alice", "password1234", "Firefox")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, "Europe/Moscow", got.Location().String())

	got, err = svc.SessionUser(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	require.NoError(t, svc.SetPasswordByLogin(ctx, "alice", "password5678"))
	_, err = svc.SessionUser(ctx, token)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "password change ends sessions")

	token, _, err = svc.Login(ctx, "alice", "password5678", "Firefox")
	require.NoError(t, err)
	_, err = svc.ChangePassword(ctx, u.ID, "password1234", "password9012", "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	_, err = svc.ChangePassword(ctx, u.ID, "password5678", "short", "Firefox")
	require.ErrorIs(t, err, errors.ErrInvalid)
	_, err = svc.SessionUser(ctx, token)
	require.NoError(t, err, "failed change keeps sessions")

	newToken, err := svc.ChangePassword(ctx, u.ID, "password5678", "password9012", "Firefox")
	require.NoError(t, err)
	_, err = svc.SessionUser(ctx, token)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "password change ends other sessions")
	got, err = svc.SessionUser(ctx, newToken)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	token, _, err = svc.Login(ctx, "alice", "password9012", "Firefox")
	require.NoError(t, err)
	require.NoError(t, svc.Logout(ctx, token))
	_, err = svc.SessionUser(ctx, token)
	require.ErrorIs(t, err, errors.ErrUnauthorized)
}

func TestSessionIdleExpiry(t *testing.T) {
	t.Parallel()

	clock := new(timex.Fixed(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
	svc, err := service.New(repotest.New(t), clock, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	a, ctx := svc.Auth, t.Context()
	_, err = a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	token, _, err := a.Login(ctx, "alice", "password1234", "")
	require.NoError(t, err)

	// used every 6 days the session lives past the idle limit
	for range 3 {
		*clock = timex.Fixed(clock.Now().Add(6 * 24 * time.Hour))
		_, err = a.SessionUser(ctx, token)
		require.NoError(t, err)
	}
	*clock = timex.Fixed(clock.Now().Add(7 * 24 * time.Hour))
	_, err = a.SessionUser(ctx, token)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "idle too long")
}

func TestAPIKeys(t *testing.T) {
	t.Parallel()

	svc := newServices(t, repotest.New(t)).Auth
	ctx := t.Context()

	u, err := svc.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)

	_, _, err = svc.CreateAPIKey(ctx, u.ID, "wrong password", "script", domain.ScopeRead, "never")
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	_, _, err = svc.CreateAPIKey(ctx, u.ID, "password1234", " ", domain.ScopeRead, "never")
	require.ErrorIs(t, err, errors.ErrInvalid)
	_, _, err = svc.CreateAPIKey(ctx, u.ID, "password1234", "script", "admin", "never")
	require.ErrorIs(t, err, errors.ErrInvalid)

	token, key, err := svc.CreateAPIKey(ctx, u.ID, "password1234", "script", domain.ScopeRead, "never")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(token, "gf_"+key.Prefix+"_"))

	got, err := svc.AuthenticateAPIKey(ctx, token, domain.ScopeRead)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.UserID)
	assert.Equal(t, domain.ScopeRead, got.Scope)

	_, err = svc.AuthenticateAPIKey(ctx, token, domain.ScopeWrite)
	require.ErrorIs(t, err, errors.ErrForbidden)

	for _, bad := range []string{"", "gf_", token + "x", "gf_" + key.Prefix + "_deadbeef"} {
		_, err = svc.AuthenticateAPIKey(ctx, bad, domain.ScopeRead)
		require.ErrorIs(t, err, errors.ErrUnauthorized, bad)
	}

	require.NoError(t, svc.RevokeAPIKey(ctx, u.ID, key.ID))
	_, err = svc.AuthenticateAPIKey(ctx, token, domain.ScopeRead)
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	require.ErrorIs(t, svc.RevokeAPIKey(ctx, u.ID, key.ID), errors.ErrNotFound, "already revoked")

	writeToken, writeKey, err := svc.CreateAPIKey(ctx, u.ID, "password1234", "agent", domain.ScopeWrite, "never")
	require.NoError(t, err)
	got, err = svc.AuthenticateAPIKey(ctx, writeToken, domain.ScopeWrite)
	require.NoError(t, err)
	assert.Equal(t, writeKey.ID, got.ID)

	other, err := svc.CreateUser(ctx, "bob", "password1234", "UTC", "user")
	require.NoError(t, err)
	require.ErrorIs(t, svc.RevokeAPIKey(ctx, other.ID, writeKey.ID), errors.ErrNotFound, "other user's key")

	keys, err := svc.ListAPIKeys(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, writeKey.ID, keys[0].ID)
	assert.True(t, keys[1].Revoked)
	assert.False(t, keys[1].LastUsedAt.IsZero())
}

func TestListRevokeSessions(t *testing.T) {
	t.Parallel()

	clock := new(timex.Fixed(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
	svc, err := service.New(repotest.New(t), clock, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	a, ctx := svc.Auth, t.Context()
	u, err := a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	bob, err := a.CreateUser(ctx, "bob", "password1234", "UTC", "user")
	require.NoError(t, err)

	old, _, err := a.Login(ctx, "alice", "password1234", "Old/1.0")
	require.NoError(t, err)
	*clock = timex.Fixed(clock.Now().Add(7 * 24 * time.Hour))
	phone, _, err := a.Login(ctx, "alice", "password1234", "  Phone/2.0 ")
	require.NoError(t, err)
	laptop, _, err := a.Login(ctx, "alice", "password1234", strings.Repeat("x", 300))
	require.NoError(t, err)

	sessions, err := a.ListSessions(ctx, u.ID, laptop)
	require.NoError(t, err)
	require.Len(t, sessions, 2, "expired session hidden")
	assert.Len(t, sessions[0].UserAgent, 200, "user agent capped")
	assert.True(t, sessions[0].Current)
	assert.Equal(t, "Phone/2.0", sessions[1].UserAgent)
	assert.False(t, sessions[1].Current)

	_, err = a.SessionUser(ctx, old)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "expired")

	require.ErrorIs(t, a.RevokeSession(ctx, bob.ID, sessions[1].ID), errors.ErrNotFound, "other user's session")
	require.NoError(t, a.RevokeSession(ctx, u.ID, sessions[1].ID))
	_, err = a.SessionUser(ctx, phone)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "revoked")
	_, err = a.SessionUser(ctx, laptop)
	require.NoError(t, err)

	n, err := a.DeleteExpiredSessions(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestAPIKeyExpiry(t *testing.T) {
	t.Parallel()

	clock := new(timex.Fixed(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
	svc, err := service.New(repotest.New(t), clock, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	a, ctx := svc.Auth, t.Context()
	u, err := a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)

	_, _, err = a.CreateAPIKey(ctx, u.ID, "password1234", "x", domain.ScopeRead, "14")
	require.ErrorIs(t, err, errors.ErrInvalid)

	week, weekKey, err := a.CreateAPIKey(ctx, u.ID, "password1234", "week", domain.ScopeRead, "7")
	require.NoError(t, err)
	assert.Equal(t, clock.Now().Add(7*24*time.Hour).Unix(), weekKey.ExpiresAt.Unix())
	forever, foreverKey, err := a.CreateAPIKey(ctx, u.ID, "password1234", "forever", domain.ScopeRead, "never")
	require.NoError(t, err)
	assert.True(t, foreverKey.ExpiresAt.IsZero())

	*clock = timex.Fixed(clock.Now().Add(7*24*time.Hour - time.Second))
	_, err = a.AuthenticateAPIKey(ctx, week, domain.ScopeRead)
	require.NoError(t, err)

	*clock = timex.Fixed(clock.Now().Add(time.Second))
	_, err = a.AuthenticateAPIKey(ctx, week, domain.ScopeRead)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "expired")

	*clock = timex.Fixed(clock.Now().Add(10 * 365 * 24 * time.Hour))
	_, err = a.AuthenticateAPIKey(ctx, forever, domain.ScopeRead)
	require.NoError(t, err)

	keys, err := a.ListAPIKeys(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.False(t, keys[0].Expired, "never expires")
	assert.True(t, keys[1].Expired)
}
