package service

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/ysomad/gigafin/internal/credential"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/password"
	"github.com/ysomad/gigafin/internal/repository"
	"github.com/ysomad/gigafin/internal/secretbox"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/totp"
)

var (
	errInvalidCredentials   = domain.NewError(errors.ErrUnauthorized, "invalid login or password")
	errWrongPassword        = domain.NewError(errors.ErrUnauthorized, "wrong password")
	errWrongCode            = domain.NewError(errors.ErrUnauthorized, "wrong code")
	errTwoFactorOn          = domain.NewError(errors.ErrConflict, "two-factor authentication is on already")
	errTwoFactorOff         = domain.NewError(errors.ErrConflict, "two-factor authentication is off")
	errNoSetup              = domain.NewError(errors.ErrConflict, "the setup is gone, start it again")
	errTwoFactorUnavailable = domain.NewError(errors.ErrConflict, "two-factor authentication is not configured")
)

// Auth manages users, browser sessions, two-factor authentication and API keys.
type Auth struct {
	store   *repository.Store
	clock   timex.Clock
	log     *slog.Logger
	session domain.SessionLifetime
	// box encrypts TOTP secrets, nil without a key, which disables setting up
	// two-factor authentication
	box *secretbox.Box
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

	if err := domain.CheckPasswordPolicy(pass); err != nil {
		return domain.User{}, domain.NewError(err, "password")
	}

	tz, err := domain.ParseTimezone(timezone)
	if err != nil {
		return domain.User{}, err
	}

	hash, err := password.Hash(pass)
	if err != nil {
		return domain.User{}, err
	}

	usr, err := s.store.Write().Users.Create(ctx, login, hash, tz, r, s.clock.Now())
	if err != nil {
		return domain.User{}, err
	}

	s.log.InfoContext(ctx, "user created", slog.Int64("user_id", usr.ID), slog.String("role", string(usr.Role)))
	return usr, nil
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

// SetPasswordByLogin sets the password of a user and ends all their sessions, revoking
// their API keys too when revokeKeys is set.
func (s *Auth) SetPasswordByLogin(ctx context.Context, login, pass string, revokeKeys bool) error {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return err
	}

	c, err := s.store.Read().Users.CredentialsByLogin(ctx, login)
	if err != nil {
		return err
	}

	return s.SetPassword(ctx, c.User.ID, pass, revokeKeys)
}

// SetPassword sets the password of a user and ends all their sessions, revoking their
// API keys too when revokeKeys is set.
func (s *Auth) SetPassword(ctx context.Context, userID int64, pass string, revokeKeys bool) error {
	if err := domain.CheckPasswordPolicy(pass); err != nil {
		return domain.NewError(err, "password")
	}

	hash, err := password.Hash(pass)
	if err != nil {
		return err
	}

	now := s.clock.Now()
	err = s.store.InTx(ctx, func(r repository.Repos) error {
		if err := r.Users.SetPassword(ctx, userID, hash, now); err != nil {
			return err
		}
		// a sign-in that passed the old password must not finish
		if err := r.Challenges.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		if revokeKeys {
			if err := r.APIKeys.RevokeAll(ctx, userID, now); err != nil {
				return err
			}
		}
		return r.Sessions.DeleteForUser(ctx, userID)
	})
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "password set, sessions ended",
		slog.Int64("user_id", userID), slog.Bool("api_keys_revoked", revokeKeys))
	return nil
}

// CheckPassword returns errWrongPassword unless password is the user's
// current password.
func (s *Auth) CheckPassword(ctx context.Context, userID int64, pass string) error {
	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return err
	}

	ok, err := password.Compare(c.PasswordHash, pass)
	if err != nil {
		return err
	}
	if !ok {
		return errWrongPassword
	}

	return nil
}

// ChangePassword checks the current password, sets a new one and ends all sessions of
// the user, revoking their API keys too when revokeKeys is set, then starts a new
// session for the browser identified by userAgent. It returns the new session token to
// put in a cookie.
func (s *Auth) ChangePassword(
	ctx context.Context, userID int64, current, pass, userAgent string, revokeKeys bool,
) (string, error) {
	if err := s.CheckPassword(ctx, userID, current); err != nil {
		return "", err
	}

	if err := domain.CheckPasswordPolicy(pass); err != nil {
		return "", domain.NewError(err, "password")
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
		if err := r.Challenges.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		if revokeKeys {
			if err := r.APIKeys.RevokeAll(ctx, userID, now); err != nil {
				return err
			}
		}
		if err := r.Sessions.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		return r.Sessions.Create(ctx, tok.Hash, userID, userAgent, s.session.Expiry(now, now), now)
	})
	if err != nil {
		return "", err
	}

	s.log.InfoContext(ctx, "password changed, other sessions ended",
		slog.Int64("user_id", userID), slog.Bool("api_keys_revoked", revokeKeys))
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

// SignIn is a passed password check of Login.
type SignIn struct {
	User domain.User
	// Token is the session token to put in a cookie, or with TwoFactor the token of a
	// challenge to pass with a code to LoginTwoFactor within domain.LoginChallengeTTL.
	Token string
	// TwoFactor is set when the user has two-factor authentication on.
	TwoFactor bool
}

// Login checks credentials and starts a session for the browser identified by
// userAgent, or a challenge when the user has two-factor authentication on.
func (s *Auth) Login(ctx context.Context, login, pass, userAgent string) (SignIn, error) {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return SignIn{}, err
	}

	cred, err := s.store.Read().Users.CredentialsByLogin(ctx, login)
	if errors.Is(err, errors.ErrNotFound) {
		// hash anyway so response time does not reveal which logins exist
		if _, err := password.Hash(pass); err != nil {
			return SignIn{}, err
		}
		return SignIn{}, errInvalidCredentials
	}

	if err != nil {
		return SignIn{}, err
	}

	ok, err := password.Compare(cred.PasswordHash, pass)
	if err != nil {
		return SignIn{}, err
	}
	if !ok {
		return SignIn{}, errInvalidCredentials
	}

	tok, err := credential.New(credential.KindSession)
	if err != nil {
		return SignIn{}, err
	}

	now := s.clock.Now()
	userAgent = domain.ParseUserAgent(userAgent)

	if len(cred.TOTPSecret) > 0 {
		expires := now.Add(domain.LoginChallengeTTL)
		err = s.store.Write().Challenges.Create(ctx, tok.Hash, cred.User.ID, userAgent, expires, now)
		if err != nil {
			return SignIn{}, err
		}
		s.log.InfoContext(ctx, "password accepted, code required", slog.Int64("user_id", cred.User.ID))
		return SignIn{User: cred.User, Token: tok.Token, TwoFactor: true}, nil
	}

	err = s.store.Write().Sessions.Create(ctx, tok.Hash, cred.User.ID, userAgent, s.session.Expiry(now, now), now)
	if err != nil {
		return SignIn{}, err
	}

	s.log.InfoContext(ctx, "user signed in", slog.Int64("user_id", cred.User.ID), slog.String("user_agent", userAgent))
	return SignIn{User: cred.User, Token: tok.Token}, nil
}

// LoginTwoFactor passes the challenge of Login with a code of the authenticator app or
// a recovery code and starts a session for the browser the challenge was made for. It
// returns the session token to put in a cookie. A wrong code fails with
// errors.ErrUnauthorized; after domain.LoginChallengeAttempts codes, or once the
// challenge expired, it fails with domain.ErrLoginExpired and the sign-in starts over.
func (s *Auth) LoginTwoFactor(ctx context.Context, challenge, code string) (string, error) {
	now := s.clock.Now()

	ch, err := s.store.Write().Challenges.TakeAttempt(ctx, credential.Hash(challenge), domain.LoginChallengeAttempts, now)
	if errors.Is(err, errors.ErrNotFound) {
		return "", domain.ErrLoginExpired
	}
	if err != nil {
		return "", err
	}

	cred, err := s.store.Read().Users.Credentials(ctx, ch.UserID)
	if err != nil {
		return "", err
	}

	ok, err := s.useSecondFactor(ctx, cred, code, now)
	if err != nil {
		return "", err
	}
	if !ok {
		// for fail2ban and alerts
		s.log.WarnContext(ctx, "second factor rejected",
			slog.Int64("user_id", ch.UserID), slog.Int("attempt", ch.Attempts))
		return "", errWrongCode
	}

	tok, err := credential.New(credential.KindSession)
	if err != nil {
		return "", err
	}

	err = s.store.InTx(ctx, func(r repository.Repos) error {
		// two requests with the same challenge start one session
		deleted, err := r.Challenges.Delete(ctx, ch.ID)
		if err != nil {
			return err
		}
		if !deleted {
			return domain.ErrLoginExpired
		}
		return r.Sessions.Create(ctx, tok.Hash, ch.UserID, ch.UserAgent, s.session.Expiry(now, now), now)
	})
	if err != nil {
		return "", err
	}

	s.log.InfoContext(ctx, "user signed in", slog.Int64("user_id", ch.UserID),
		slog.String("user_agent", ch.UserAgent), slog.Bool("two_factor", true))
	return tok.Token, nil
}

// useSecondFactor reports whether code is a TOTP code of cred not used before or one of
// its recovery codes, and uses it up. Without a key only recovery codes work.
func (s *Auth) useSecondFactor(
	ctx context.Context, cred repository.Credentials, code string, now time.Time,
) (bool, error) {
	if len(cred.TOTPSecret) == 0 {
		return false, errTwoFactorOff
	}
	if s.box != nil {
		secret, err := s.openTOTP(cred.User.ID, cred.TOTPSecret)
		if err != nil {
			return false, err
		}
		if step, ok := totp.Verify(secret, code, now, cred.TOTPLastStep); ok {
			return s.store.Write().Users.UseTOTPStep(ctx, cred.User.ID, step)
		}
	}
	prefix, code, ok := credential.ParseRecoveryCode(code)
	if !ok {
		return false, nil
	}
	codes, err := s.store.Read().Recovery.ListByPrefix(ctx, cred.User.ID, prefix)
	if err != nil {
		return false, err
	}
	for _, c := range codes {
		match, err := password.Compare(c.Hash, code)
		if err != nil {
			return false, err
		}
		if !match {
			continue
		}
		// of two requests with the same code only one deletes it
		ok, err := s.store.Write().Recovery.Delete(ctx, c.ID)
		if ok {
			s.log.InfoContext(ctx, "recovery code used", slog.Int64("user_id", cred.User.ID))
		}
		return ok, err
	}
	return false, nil
}

// TwoFactor returns the state of the user's two-factor authentication.
func (s *Auth) TwoFactor(ctx context.Context, userID int64) (domain.TwoFactor, error) {
	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return domain.TwoFactor{}, err
	}
	state := domain.TwoFactor{Available: s.box != nil}
	if len(c.TOTPSecret) == 0 {
		if len(c.TOTPPendingSecret) > 0 && s.box != nil {
			if state.PendingSecret, err = s.openTOTP(userID, c.TOTPPendingSecret); err != nil {
				return domain.TwoFactor{}, err
			}
		}
		return state, nil
	}
	n, err := s.store.Read().Recovery.Count(ctx, userID)
	if err != nil {
		return domain.TwoFactor{}, err
	}
	state.Enabled, state.EnabledAt, state.RecoveryCodes = true, c.TOTPEnabledAt, n
	return state, nil
}

// SetupTwoFactor starts setting up two-factor authentication: it stores a new secret,
// replacing one set up before, and returns it to show in the authenticator app. It is
// kept on the server, so EnableTwoFactor turns on the secret the user was shown.
func (s *Auth) SetupTwoFactor(ctx context.Context, userID int64) (string, error) {
	if s.box == nil {
		return "", errTwoFactorUnavailable
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return "", err
	}
	sealed, err := s.sealTOTP(userID, secret)
	if err != nil {
		return "", err
	}
	err = s.store.Write().Users.SetPendingTOTP(ctx, userID, sealed, s.clock.Now())
	if errors.Is(err, errors.ErrNotFound) {
		return "", errTwoFactorOn
	}
	if err != nil {
		return "", err
	}
	return secret, nil
}

// CancelTwoFactorSetup drops the secret of SetupTwoFactor.
func (s *Auth) CancelTwoFactorSetup(ctx context.Context, userID int64) error {
	return s.store.Write().Users.ClearPendingTOTP(ctx, userID, s.clock.Now())
}

// EnableTwoFactor checks the user's current password and a code of the secret of
// SetupTwoFactor, which proves the authenticator app has it, then turns two-factor
// authentication on. Sessions started with the password alone end and the browser
// identified by userAgent gets a new one. It returns domain.RecoveryCodeCount recovery
// codes, shown once, and the session token to put in a cookie.
func (s *Auth) EnableTwoFactor(
	ctx context.Context, userID int64, pass, code, userAgent string,
) (codes []string, token string, err error) {
	if err := s.CheckPassword(ctx, userID, pass); err != nil {
		return nil, "", err
	}
	if s.box == nil {
		return nil, "", errTwoFactorUnavailable
	}

	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return nil, "", err
	}
	if len(c.TOTPSecret) > 0 {
		return nil, "", errTwoFactorOn
	}
	if len(c.TOTPPendingSecret) == 0 {
		return nil, "", errNoSetup
	}
	secret, err := s.openTOTP(userID, c.TOTPPendingSecret)
	if err != nil {
		return nil, "", err
	}

	now := s.clock.Now()

	step, ok := totp.Verify(secret, code, now, 0)
	if !ok {
		return nil, "", errWrongCode
	}

	recovery, err := newRecoveryCodes()
	if err != nil {
		return nil, "", err
	}
	tok, err := credential.New(credential.KindSession)
	if err != nil {
		return nil, "", err
	}
	userAgent = domain.ParseUserAgent(userAgent)

	err = s.store.InTx(ctx, func(r repository.Repos) error {
		// a setup started meanwhile replaced the secret the code is of
		if err := r.Users.EnableTOTP(ctx, userID, c.TOTPPendingSecret, step, now); errors.Is(err, errors.ErrNotFound) {
			return errNoSetup
		} else if err != nil {
			return err
		}
		if err := r.Recovery.Replace(ctx, userID, recovery, now); err != nil {
			return err
		}
		if err := r.Sessions.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		return r.Sessions.Create(ctx, tok.Hash, userID, userAgent, s.session.Expiry(now, now), now)
	})
	if err != nil {
		return nil, "", err
	}

	s.log.InfoContext(ctx, "two-factor authentication enabled, other sessions ended", slog.Int64("user_id", userID))
	return recoveryTokens(recovery), tok.Token, nil
}

// DisableTwoFactor checks the user's current password and a code of the authenticator
// app or a recovery code, then turns two-factor authentication off.
func (s *Auth) DisableTwoFactor(ctx context.Context, userID int64, pass, code string) error {
	if err := s.CheckPassword(ctx, userID, pass); err != nil {
		return err
	}

	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return err
	}

	ok, err := s.useSecondFactor(ctx, c, code, s.clock.Now())
	if err != nil {
		return err
	}
	if !ok {
		return errWrongCode
	}

	return s.disableTwoFactor(ctx, userID)
}

// DisableTwoFactorByLogin turns two-factor authentication of a user off without a code,
// for a user who lost both the authenticator app and the recovery codes.
func (s *Auth) DisableTwoFactorByLogin(ctx context.Context, login string) error {
	login, err := domain.ParseLogin(login)
	if err != nil {
		return err
	}

	c, err := s.store.Read().Users.CredentialsByLogin(ctx, login)
	if err != nil {
		return err
	}
	if len(c.TOTPSecret) == 0 {
		return errTwoFactorOff
	}

	return s.disableTwoFactor(ctx, c.User.ID)
}

func (s *Auth) disableTwoFactor(ctx context.Context, userID int64) error {
	err := s.store.InTx(ctx, func(r repository.Repos) error {
		if err := r.Users.DisableTOTP(ctx, userID, s.clock.Now()); err != nil {
			return err
		}
		if err := r.Recovery.DeleteForUser(ctx, userID); err != nil {
			return err
		}
		return r.Challenges.DeleteForUser(ctx, userID)
	})
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "two-factor authentication disabled", slog.Int64("user_id", userID))
	return nil
}

// RegenerateRecoveryCodes checks the user's current password and a code of the
// authenticator app or a recovery code, then replaces their recovery codes with
// domain.RecoveryCodeCount new ones, shown once.
func (s *Auth) RegenerateRecoveryCodes(ctx context.Context, userID int64, pass, code string) ([]string, error) {
	if err := s.CheckPassword(ctx, userID, pass); err != nil {
		return nil, err
	}

	c, err := s.store.Read().Users.Credentials(ctx, userID)
	if err != nil {
		return nil, err
	}

	ok, err := s.useSecondFactor(ctx, c, code, s.clock.Now())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errWrongCode
	}

	recovery, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}

	if err := s.store.InTx(ctx, func(r repository.Repos) error {
		return r.Recovery.Replace(ctx, userID, recovery, s.clock.Now())
	}); err != nil {
		return nil, err
	}

	s.log.InfoContext(ctx, "recovery codes regenerated", slog.Int64("user_id", userID))
	return recoveryTokens(recovery), nil
}

// newRecoveryCodes returns domain.RecoveryCodeCount recovery codes.
func newRecoveryCodes() ([]credential.Token, error) {
	codes := make([]credential.Token, domain.RecoveryCodeCount)
	for i := range codes {
		var err error
		if codes[i], err = credential.New(credential.KindRecovery); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// recoveryTokens returns the codes to show of tokens.
func recoveryTokens(tokens []credential.Token) []string {
	codes := make([]string, len(tokens))
	for i, t := range tokens {
		codes[i] = t.Token
	}
	return codes
}

// sealTOTP encrypts a TOTP secret of the user; the user id binds it to the row.
func (s *Auth) sealTOTP(userID int64, secret string) ([]byte, error) {
	return s.box.Seal("totp:"+strconv.FormatInt(userID, 10), []byte(secret))
}

// openTOTP decrypts a secret sealed by sealTOTP.
func (s *Auth) openTOTP(userID int64, sealed []byte) (string, error) {
	b, err := s.box.Open("totp:"+strconv.FormatInt(userID, 10), sealed)
	return string(b), err
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

// DeleteExpiredSessions removes expired sessions and sign-in challenges and returns how
// many.
func (s *Auth) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	now := s.clock.Now()
	n, err := s.store.Write().Sessions.DeleteExpired(ctx, now)
	if err != nil {
		return n, err
	}
	c, err := s.store.Write().Challenges.DeleteExpired(ctx, now)
	return n + c, err
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
		slog.String("key_prefix", k.Prefix),
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
		s.log.WarnContext(ctx, "api key rejected",
			slog.String("reason", "wrong secret"),
			slog.String("key_prefix", prefix),
			slog.Int64("key_id", k.Key.ID),
		)
		return domain.APIKey{}, errors.ErrUnauthorized
	}

	if !k.Key.Live(now) {
		s.log.WarnContext(ctx, "api key rejected",
			slog.String("reason", "revoked or expired"),
			slog.String("key_prefix", prefix),
			slog.Int64("key_id", k.Key.ID),
		)
		return domain.APIKey{}, errors.ErrUnauthorized
	}

	if !k.Key.Scope.Allows(want) {
		s.log.WarnContext(
			ctx,
			"api key rejected",
			slog.String("reason", "scope"),
			slog.String("key_prefix", prefix),
			slog.Int64("key_id", k.Key.ID),
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
