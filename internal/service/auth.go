package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ysomad/gigafin/internal/credential"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/password"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/timex"
)

var errInvalidCredentials = domain.NewError(errors.ErrUnauthorized, "invalid login or password")

// Auth manages users, browser sessions and API keys.
type Auth struct {
	store   *repository.Store
	clock   timex.Clock
	log     *slog.Logger
	session domain.SessionLifetime
}

// CreateUser creates a user with role.
func (s *Auth) CreateUser(ctx context.Context, login, pass, timezone, role string) (domain.User, error) {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return domain.User{}, err
	}
	r, err := domain.ParseRole(role)
	if err != nil {
		return domain.User{}, err
	}
	if err := password.CheckPolicy(pass); err != nil {
		return domain.User{}, err
	}
	tz, err := domain.ParseTimezone(timezone)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := password.Hash(pass)
	if err != nil {
		return domain.User{}, err
	}
	u, err := s.store.Write().Users.Create(ctx, login, hash, tz, r, s.clock.Now())
	if err != nil {
		return domain.User{}, err
	}
	s.log.InfoContext(ctx, "user created", slog.Int64("user_id", u.ID), slog.String("role", string(u.Role)))
	return u, nil
}

// SetRoleByLogin sets the role of a user. It applies to the user's next request.
func (s *Auth) SetRoleByLogin(ctx context.Context, login, role string) error {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return err
	}
	r, err := domain.ParseRole(role)
	if err != nil {
		return err
	}
	if err := s.store.Write().Users.SetRoleByLogin(ctx, login, r, s.clock.Now()); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "user role set", slog.String("login", login), slog.String("role", string(r)))
	return nil
}

// SetPasswordByLogin sets the password of a user and ends all their sessions.
func (s *Auth) SetPasswordByLogin(ctx context.Context, login, pass string) error {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return err
	}
	c, err := s.store.Read().Users.CredentialsByLogin(ctx, login)
	if err != nil {
		return err
	}
	return s.SetPassword(ctx, c.User.ID, pass)
}

// SetPassword sets the password of a user and ends all their sessions.
func (s *Auth) SetPassword(ctx context.Context, userID int64, pass string) error {
	if err := password.CheckPolicy(pass); err != nil {
		return err
	}
	hash, err := password.Hash(pass)
	if err != nil {
		return err
	}
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		if err := r.Users.SetPassword(ctx, userID, hash, s.clock.Now()); err != nil {
			return err
		}
		return r.Sessions.DeleteForUser(ctx, userID)
	})
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "password set, sessions ended", slog.Int64("user_id", userID))
	return nil
}

// CheckPassword returns errInvalidCredentials unless password is the user's
// current password.
func (s *Auth) CheckPassword(ctx context.Context, userID int64, pass string) error {
	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return err
	}
	ok, err := password.Check(c.PasswordHash, pass)
	if err != nil {
		return err
	}
	if !ok {
		return errInvalidCredentials
	}
	return nil
}

// ChangePassword checks the current password, sets a new one and ends all sessions of
// the user, then starts a new one for the browser identified by userAgent. It returns
// the new session token to put in a cookie.
func (s *Auth) ChangePassword(ctx context.Context, userID int64, current, pass, userAgent string) (string, error) {
	if err := s.CheckPassword(ctx, userID, current); err != nil {
		return "", err
	}
	if err := password.CheckPolicy(pass); err != nil {
		return "", err
	}
	hash, err := password.Hash(pass)
	if err != nil {
		return "", err
	}
	tok, err := credential.New(credential.KindSession)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	userAgent = domain.ParseUserAgent(userAgent)
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		if err := r.Users.SetPassword(ctx, userID, hash, now); err != nil {
			return err
		}
		if err := r.Sessions.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		return r.Sessions.Create(ctx, tok.Hash, userID, userAgent, s.session.Expiry(now, now), now)
	})
	if err != nil {
		return "", err
	}
	s.log.InfoContext(ctx, "password changed, other sessions ended", slog.Int64("user_id", userID))
	return tok.Token, nil
}

// SetTimezone changes the user's time zone.
func (s *Auth) SetTimezone(ctx context.Context, userID int64, timezone string) error {
	tz, err := domain.ParseTimezone(timezone)
	if err != nil {
		return err
	}
	if err := s.store.Write().Users.SetTimezone(ctx, userID, tz, s.clock.Now()); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "timezone set", slog.Int64("user_id", userID), slog.String("timezone", tz))
	return nil
}

// Login checks credentials and starts a session for the browser identified by
// userAgent. It returns the session token to put in a cookie.
func (s *Auth) Login(ctx context.Context, login, pass, userAgent string) (string, domain.User, error) {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return "", domain.User{}, err
	}
	c, err := s.store.Read().Users.CredentialsByLogin(ctx, login)
	if errors.Is(err, errors.ErrNotFound) {
		// hash anyway so response time does not reveal which logins exist
		if _, err := password.Hash(pass); err != nil {
			return "", domain.User{}, err
		}
		return "", domain.User{}, errInvalidCredentials
	}
	if err != nil {
		return "", domain.User{}, err
	}
	ok, err := password.Check(c.PasswordHash, pass)
	if err != nil {
		return "", domain.User{}, err
	}
	if !ok {
		return "", domain.User{}, errInvalidCredentials
	}
	tok, err := credential.New(credential.KindSession)
	if err != nil {
		return "", domain.User{}, err
	}
	now := s.clock.Now()
	userAgent = domain.ParseUserAgent(userAgent)
	err = s.store.Write().Sessions.Create(ctx, tok.Hash, c.User.ID, userAgent, s.session.Expiry(now, now), now)
	if err != nil {
		return "", domain.User{}, err
	}
	s.log.InfoContext(ctx, "user signed in", slog.Int64("user_id", c.User.ID), slog.String("user_agent", userAgent))
	return tok.Token, c.User, nil
}

// SessionUser returns the user of a live session or errors.ErrUnauthorized, and
// extends the session's idle expiry.
func (s *Auth) SessionUser(ctx context.Context, tok string) (domain.User, error) {
	if tok == "" {
		return domain.User{}, errors.ErrUnauthorized
	}
	hash, now := credential.Hash(tok), s.clock.Now()
	sess, err := s.store.Read().Sessions.Get(ctx, hash)
	if errors.Is(err, errors.ErrNotFound) {
		return domain.User{}, errors.ErrUnauthorized
	}
	if err != nil {
		return domain.User{}, err
	}
	if !sess.Live(now) {
		return domain.User{}, errors.ErrUnauthorized
	}
	if exp, ok := sess.Touch(now, s.session); ok {
		if err := s.store.Write().Sessions.Touch(ctx, sess.ID, exp, now); err != nil {
			return domain.User{}, err
		}
	}
	return sess.User, nil
}

// Logout ends a session.
func (s *Auth) Logout(ctx context.Context, tok string) error {
	return s.store.Write().Sessions.Delete(ctx, credential.Hash(tok))
}

// ListSessions returns the user's live sessions, newest first, with the one of token
// marked current.
func (s *Auth) ListSessions(ctx context.Context, userID int64, token string) ([]domain.Session, error) {
	all, err := s.store.Read().Sessions.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	var current int64
	if token != "" {
		cur, err := s.store.Read().Sessions.Get(ctx, credential.Hash(token))
		if err != nil && !errors.Is(err, errors.ErrNotFound) {
			return nil, err
		}
		current = cur.ID
	}
	now := s.clock.Now()
	live := make([]domain.Session, 0, len(all))
	for _, sess := range all {
		if !sess.Live(now) {
			continue
		}
		sess.Current = sess.ID == current
		live = append(live, sess)
	}
	return live, nil
}

// RevokeSession ends one of the user's sessions.
func (s *Auth) RevokeSession(ctx context.Context, userID, id int64) error {
	if err := s.store.Write().Sessions.DeleteByID(ctx, userID, id); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "session revoked", slog.Int64("user_id", userID), slog.Int64("session_id", id))
	return nil
}

// DeleteExpiredSessions removes expired sessions and returns how many.
func (s *Auth) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	return s.store.Write().Sessions.DeleteExpired(ctx, s.clock.Now())
}

// CreateAPIKey checks the user's current password, since a key outlives the session,
// creates an API key living for ttl (see domain.ParseKeyTTL) and returns its secret token,
// which is shown once.
func (s *Auth) CreateAPIKey(
	ctx context.Context, userID int64, pass, name string, scope domain.Scope, ttl string,
) (string, domain.APIKey, error) {
	if err := s.CheckPassword(ctx, userID, pass); err != nil {
		return "", domain.APIKey{}, err
	}
	name, err := domain.ParseAPIKeyName(name)
	if err != nil {
		return "", domain.APIKey{}, err
	}
	if scope, err = domain.ParseScope(string(scope)); err != nil {
		return "", domain.APIKey{}, err
	}
	life, err := domain.ParseKeyTTL(ttl)
	if err != nil {
		return "", domain.APIKey{}, err
	}
	tok, err := credential.New(credential.KindAPIKey)
	if err != nil {
		return "", domain.APIKey{}, err
	}
	now := s.clock.Now()
	var expires time.Time
	if life > 0 {
		expires = now.Add(life)
	}
	k, err := s.store.Write().APIKeys.Create(ctx, userID, name, scope, tok, expires, now)
	if err != nil {
		return "", domain.APIKey{}, err
	}
	s.log.InfoContext(
		ctx,
		"api key created",
		slog.Int64("user_id", userID),
		slog.Int64("key_id", k.ID),
		slog.String(
			"key_prefix",
			k.Prefix,
		),
		slog.String("scope", string(k.Scope)),
		slog.Time("expires_at", k.ExpiresAt),
	)
	return tok.Token, k, nil
}

// ListAPIKeys returns the user's API keys, newest first, with expired ones marked.
func (s *Auth) ListAPIKeys(ctx context.Context, userID int64) ([]domain.APIKey, error) {
	keys, err := s.store.Read().APIKeys.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	for i, k := range keys {
		keys[i].Expired = k.ExpiredAt(now)
	}
	return keys, nil
}

// RevokeAPIKey revokes one of the user's live API keys.
func (s *Auth) RevokeAPIKey(ctx context.Context, userID, id int64) error {
	if err := s.store.Write().APIKeys.Revoke(ctx, userID, id, s.clock.Now()); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "api key revoked", slog.Int64("user_id", userID), slog.Int64("key_id", id))
	return nil
}

// AuthenticateAPIKey checks an API key token and returns its key, or
// errors.ErrUnauthorized. A key whose scope does not allow want fails with
// errors.ErrForbidden.
func (s *Auth) AuthenticateAPIKey(ctx context.Context, tok string, want domain.Scope) (domain.APIKey, error) {
	// rejections log the public prefix of the key, never the token; for fail2ban and alerts
	prefix, ok := credential.Parse(tok)
	if !ok {
		s.log.WarnContext(ctx, "api key rejected", slog.String("reason", "malformed"))
		return domain.APIKey{}, errors.ErrUnauthorized
	}
	k, err := s.store.Read().APIKeys.ByPrefix(ctx, prefix)
	if errors.Is(err, errors.ErrNotFound) {
		s.log.WarnContext(ctx, "api key rejected", slog.String("reason", "unknown"), slog.String("key_prefix", prefix))
		return domain.APIKey{}, errors.ErrUnauthorized
	}
	if err != nil {
		return domain.APIKey{}, err
	}
	now := s.clock.Now()
	if !credential.Matches(tok, k.Hash) {
		s.log.WarnContext(ctx, "api key rejected", slog.String("reason", "wrong secret"),
			slog.String("key_prefix", prefix), slog.Int64("key_id", k.Key.ID))
		return domain.APIKey{}, errors.ErrUnauthorized
	}
	if !k.Key.Live(now) {
		s.log.WarnContext(ctx, "api key rejected", slog.String("reason", "revoked or expired"),
			slog.String("key_prefix", prefix), slog.Int64("key_id", k.Key.ID))
		return domain.APIKey{}, errors.ErrUnauthorized
	}
	if !k.Key.Scope.Allows(want) {
		s.log.WarnContext(
			ctx,
			"api key rejected",
			slog.String("reason", "scope"),
			slog.String("key_prefix", prefix),
			slog.Int64(
				"key_id",
				k.Key.ID,
			),
			slog.String("scope", string(k.Key.Scope)),
			slog.String("want", string(want)),
		)
		return domain.APIKey{}, domain.NewError(
			errors.ErrForbidden,
			"api key scope %q does not allow %s",
			k.Key.Scope,
			want,
		)
	}
	if k.Key.Touch(now) {
		if err := s.store.Write().APIKeys.Touch(ctx, k.Key.ID, now); err != nil {
			return domain.APIKey{}, err
		}
	}
	return k.Key, nil
}
