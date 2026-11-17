package service_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/totp"
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

	_, err = svc.Login(ctx, "alice", "password5678", "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	_, err = svc.Login(ctx, "bob", "password1234", "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)

	require.NoError(t, svc.CheckPassword(ctx, u.ID, "password1234"))
	require.ErrorIs(t, svc.CheckPassword(ctx, u.ID, "password5678"), errors.ErrUnauthorized)

	in, err := svc.Login(ctx, "alice", "password1234", "Firefox")
	require.NoError(t, err)
	assert.False(t, in.TwoFactor)
	assert.Equal(t, u.ID, in.User.ID)
	assert.Equal(t, "Europe/Moscow", in.User.Location().String())

	got, err := svc.SessionUser(ctx, in.Token)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	token := in.Token

	require.NoError(t, svc.SetPasswordByLogin(ctx, "alice", "password5678"))
	_, err = svc.SessionUser(ctx, token)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "password change ends sessions")

	in, err = svc.Login(ctx, "alice", "password5678", "Firefox")
	require.NoError(t, err)
	token = in.Token
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

	in, err = svc.Login(ctx, "alice", "password9012", "Firefox")
	require.NoError(t, err)
	token = in.Token
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
	in, err := a.Login(ctx, "alice", "password1234", "")
	require.NoError(t, err)
	token := in.Token

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
	require.Len(t, keys, 1, "revoked key is not listed")
	assert.Equal(t, writeKey.ID, keys[0].ID)
	assert.False(t, keys[0].LastUsedAt.IsZero())
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

	in, err := a.Login(ctx, "alice", "password1234", "Old/1.0")
	require.NoError(t, err)
	old := in.Token
	*clock = timex.Fixed(clock.Now().Add(7 * 24 * time.Hour))
	in, err = a.Login(ctx, "alice", "password1234", "  Phone/2.0 ")
	require.NoError(t, err)
	phone := in.Token
	in, err = a.Login(ctx, "alice", "password1234", strings.Repeat("x", 300))
	require.NoError(t, err)
	laptop := in.Token

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

func TestTwoFactor(t *testing.T) {
	t.Parallel()

	clock := new(timex.Fixed(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
	svc, err := service.New(repotest.New(t), clock, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
		TOTPKey: bytes.Repeat([]byte{7}, 32),
	})
	require.NoError(t, err)
	a, ctx := svc.Auth, t.Context()
	u, err := a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)

	code := func(secret string) string {
		c, err := totp.Code(secret, totp.Step(clock.Now()))
		require.NoError(t, err)
		return c
	}
	// next moves the clock to the next TOTP step, since a code works once
	next := func() { *clock = timex.Fixed(clock.Now().Add(totp.Period)) }

	in, err := a.Login(ctx, "alice", "password1234", "Old")
	require.NoError(t, err)
	before := in.Token

	_, _, err = a.EnableTwoFactor(ctx, u.ID, "password1234", "000000", "Firefox")
	require.ErrorIs(t, err, errors.ErrConflict, "no setup")

	old, err := a.SetupTwoFactor(ctx, u.ID)
	require.NoError(t, err)
	secret, err := a.SetupTwoFactor(ctx, u.ID)
	require.NoError(t, err)
	require.NotEqual(t, old, secret)
	state, err := a.TwoFactor(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TwoFactor{Available: true, PendingSecret: secret}, state)

	_, _, err = a.EnableTwoFactor(ctx, u.ID, "wrong password", code(secret), "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized)
	_, _, err = a.EnableTwoFactor(ctx, u.ID, "password1234", code(old), "Firefox")
	require.ErrorIs(t, err, errors.ErrUnauthorized, "a new setup replaces the secret")

	c := code(secret)
	codes, token, err := a.EnableTwoFactor(ctx, u.ID, "password1234", c[:3]+" "+c[3:], "Firefox")
	require.NoError(t, err)
	require.Len(t, codes, domain.RecoveryCodeCount)
	_, err = a.SessionUser(ctx, before)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "sessions of the password alone end")
	_, err = a.SessionUser(ctx, token)
	require.NoError(t, err)
	_, err = a.SetupTwoFactor(ctx, u.ID)
	require.ErrorIs(t, err, errors.ErrConflict)

	state, err = a.TwoFactor(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, state.Enabled)
	assert.Empty(t, state.PendingSecret)
	assert.Equal(t, domain.RecoveryCodeCount, state.RecoveryCodes)

	in, err = a.Login(ctx, "alice", "password1234", "Firefox")
	require.NoError(t, err)
	require.True(t, in.TwoFactor)
	_, err = a.SessionUser(ctx, in.Token)
	require.ErrorIs(t, err, errors.ErrUnauthorized, "a challenge is no session")

	_, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
	require.ErrorIs(t, err, errors.ErrUnauthorized, "the code that enabled 2fa is used")

	next()
	token, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
	require.NoError(t, err)
	got, err := a.SessionUser(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	_, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
	require.ErrorIs(t, err, domain.ErrLoginExpired, "a challenge is passed once")

	// recovery code
	{
		in, err := a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		_, err = a.LoginTwoFactor(ctx, in.Token, strings.ToUpper(codes[0]))
		require.NoError(t, err)

		in, err = a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		_, err = a.LoginTwoFactor(ctx, in.Token, codes[0])
		require.ErrorIs(t, err, errors.ErrUnauthorized, "a recovery code works once")

		state, err := a.TwoFactor(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.RecoveryCodeCount-1, state.RecoveryCodes)
	}

	// attempts, also when tried at once
	{
		in, err := a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wrong := 0
		for range 3 * domain.LoginChallengeAttempts {
			wg.Go(func() {
				_, err := a.LoginTwoFactor(ctx, in.Token, "000000")
				if errors.Is(err, errors.ErrUnauthorized) && !errors.Is(err, domain.ErrLoginExpired) {
					mu.Lock()
					wrong++
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		assert.Equal(t, domain.LoginChallengeAttempts, wrong)
		next()
		_, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
		require.ErrorIs(t, err, domain.ErrLoginExpired)
	}

	// expiry
	{
		in, err := a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		*clock = timex.Fixed(clock.Now().Add(domain.LoginChallengeTTL))
		_, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
		require.ErrorIs(t, err, domain.ErrLoginExpired)
	}

	// a password change ends sign-ins in progress
	{
		in, err := a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		require.NoError(t, a.SetPassword(ctx, u.ID, "password1234"))
		next()
		_, err = a.LoginTwoFactor(ctx, in.Token, code(secret))
		require.ErrorIs(t, err, domain.ErrLoginExpired)
	}

	// recovery codes regenerated
	{
		next()
		_, err := a.RegenerateRecoveryCodes(ctx, u.ID, "wrong password", code(secret))
		require.ErrorIs(t, err, errors.ErrUnauthorized)
		_, err = a.RegenerateRecoveryCodes(ctx, u.ID, "password1234", "000000")
		require.ErrorIs(t, err, errors.ErrUnauthorized, "a code is required")
		fresh, err := a.RegenerateRecoveryCodes(ctx, u.ID, "password1234", code(secret))
		require.NoError(t, err)
		require.Len(t, fresh, domain.RecoveryCodeCount)

		in, err := a.Login(ctx, "alice", "password1234", "Firefox")
		require.NoError(t, err)
		_, err = a.LoginTwoFactor(ctx, in.Token, codes[1])
		require.ErrorIs(t, err, errors.ErrUnauthorized, "old codes are gone")
		_, err = a.LoginTwoFactor(ctx, in.Token, fresh[0])
		require.NoError(t, err)
	}

	next()
	require.ErrorIs(t, a.DisableTwoFactor(ctx, u.ID, "password1234", "000000"), errors.ErrUnauthorized)
	require.NoError(t, a.DisableTwoFactor(ctx, u.ID, "password1234", code(secret)))
	state, err = a.TwoFactor(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TwoFactor{Available: true}, state)

	in, err = a.Login(ctx, "alice", "password1234", "Firefox")
	require.NoError(t, err)
	assert.False(t, in.TwoFactor)

	require.ErrorIs(t, a.DisableTwoFactorByLogin(ctx, "alice"), errors.ErrConflict)
	next()
	secret, err = a.SetupTwoFactor(ctx, u.ID)
	require.NoError(t, err)
	require.NoError(t, a.CancelTwoFactorSetup(ctx, u.ID))
	_, _, err = a.EnableTwoFactor(ctx, u.ID, "password1234", code(secret), "Firefox")
	require.ErrorIs(t, err, errors.ErrConflict, "a canceled setup is gone")
	secret, err = a.SetupTwoFactor(ctx, u.ID)
	require.NoError(t, err)
	_, _, err = a.EnableTwoFactor(ctx, u.ID, "password1234", code(secret), "Firefox")
	require.NoError(t, err)
	require.NoError(t, a.DisableTwoFactorByLogin(ctx, "alice"))
}

func TestTwoFactorWithoutKey(t *testing.T) {
	t.Parallel()

	a, ctx := newServices(t, repotest.New(t)).Auth, t.Context()
	u, err := a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)

	state, err := a.TwoFactor(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, state.Available)
	_, err = a.SetupTwoFactor(ctx, u.ID)
	require.ErrorIs(t, err, errors.ErrConflict)
}
