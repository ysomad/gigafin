package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ysomad/gigafin/internal/credential"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/gen/sqlc"
)

// Users stores users.
type Users struct {
	q *sqlc.Queries
}

// Credentials is a user with the password hash and TOTP secret, which never leave the
// auth service.
type Credentials struct {
	User         domain.User
	PasswordHash string
	// TOTPSecret is the encrypted TOTP secret, empty when two-factor authentication is
	// off. TOTPPendingSecret is the encrypted secret being set up, empty when none is.
	TOTPSecret        []byte
	TOTPPendingSecret []byte
	// TOTPLastStep is the step of the last TOTP code used, 0 when none was.
	TOTPLastStep  int64
	TOTPEnabledAt time.Time
}

// Create stores a new user.
func (r *Users) Create(
	ctx context.Context, login, passwordHash, tz string, role domain.Role, now time.Time,
) (domain.User, error) {
	u, err := r.q.CreateUser(ctx, sqlc.CreateUserParams{
		Login: login, PasswordHash: passwordHash, Timezone: tz, Role: string(role), CreatedAt: now.Unix(),
	})
	if k, ok := constraint(err); ok {
		return domain.User{}, fmt.Errorf("user %q: %w", login, k)
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("create user: %w", err)
	}
	return domain.NewUser(u.ID, u.Login, u.Timezone, domain.Role(u.Role)), nil
}

// List returns all users.
func (r *Users) List(ctx context.Context) ([]domain.User, error) {
	rows, err := r.q.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]domain.User, len(rows))
	for i, u := range rows {
		out[i] = domain.NewUser(u.ID, u.Login, u.Timezone, domain.Role(u.Role))
	}
	return out, nil
}

// Credentials returns a user by id with the password hash.
func (r *Users) Credentials(ctx context.Context, id int64) (Credentials, error) {
	u, err := r.q.GetUser(ctx, id)
	if err != nil {
		return Credentials{}, notFound(err, "user")
	}
	return credentialsFromRow(u), nil
}

// CredentialsByLogin returns a user by login with the password hash.
func (r *Users) CredentialsByLogin(ctx context.Context, login string) (Credentials, error) {
	u, err := r.q.GetUserByLogin(ctx, login)
	if err != nil {
		return Credentials{}, notFound(err, "user")
	}
	return credentialsFromRow(u), nil
}

func credentialsFromRow(u sqlc.User) Credentials {
	c := Credentials{
		User:              domain.NewUser(u.ID, u.Login, u.Timezone, domain.Role(u.Role)),
		PasswordHash:      u.PasswordHash,
		TOTPSecret:        u.TOTPSecret,
		TOTPPendingSecret: u.TOTPPendingSecret,
		TOTPLastStep:      u.TOTPLastStep.Int64,
	}
	if u.TOTPEnabledAt.Valid {
		c.TOTPEnabledAt = time.Unix(u.TOTPEnabledAt.Int64, 0)
	}
	return c
}

// SetPassword stores a new password hash.
func (r *Users) SetPassword(ctx context.Context, id int64, hash string, now time.Time) error {
	n, err := r.q.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{
		PasswordHash: hash, UpdatedAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: id,
	})
	return affected(n, err, "update password")
}

// SetRoleByLogin stores the role of the user with login.
func (r *Users) SetRoleByLogin(ctx context.Context, login string, role domain.Role, now time.Time) error {
	n, err := r.q.UpdateUserRoleByLogin(ctx, sqlc.UpdateUserRoleByLoginParams{
		Role: string(role), UpdatedAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, Login: login,
	})
	return affected(n, err, "update role")
}

// SetTimezone stores the user's time zone.
func (r *Users) SetTimezone(ctx context.Context, id int64, tz string, now time.Time) error {
	n, err := r.q.UpdateUserTimezone(ctx, sqlc.UpdateUserTimezoneParams{
		Timezone: tz, UpdatedAt: sql.NullInt64{Int64: now.Unix(), Valid: true}, ID: id,
	})
	return affected(n, err, "update time zone")
}

// SetPendingTOTP stores the encrypted secret being set up, replacing one set up before.
// It fails with errors.ErrNotFound when two-factor authentication is on already.
func (r *Users) SetPendingTOTP(ctx context.Context, id int64, secret []byte, now time.Time) error {
	n, err := r.q.SetUserPendingTOTP(ctx, sqlc.SetUserPendingTOTPParams{
		TOTPPendingSecret: secret, UpdatedAt: nullUnix(now), ID: id,
	})
	return affected(n, err, "set pending totp")
}

// ClearPendingTOTP drops the secret being set up.
func (r *Users) ClearPendingTOTP(ctx context.Context, id int64, now time.Time) error {
	err := r.q.ClearUserPendingTOTP(ctx, sqlc.ClearUserPendingTOTPParams{UpdatedAt: nullUnix(now), ID: id})
	if err != nil {
		return fmt.Errorf("clear pending totp: %w", err)
	}
	return nil
}

// EnableTOTP turns two-factor authentication on with the pending secret, which must
// still be pending, recording step as used. It fails with errors.ErrNotFound when it is
// on already or another secret is pending.
func (r *Users) EnableTOTP(ctx context.Context, id int64, pending []byte, step int64, now time.Time) error {
	n, err := r.q.EnableUserTOTP(ctx, sqlc.EnableUserTOTPParams{
		Step:    sql.NullInt64{Int64: step, Valid: true},
		Now:     nullUnix(now),
		ID:      id,
		Pending: pending,
	})
	return affected(n, err, "enable totp")
}

// DisableTOTP turns two-factor authentication off.
func (r *Users) DisableTOTP(ctx context.Context, id int64, now time.Time) error {
	n, err := r.q.DisableUserTOTP(ctx, sqlc.DisableUserTOTPParams{UpdatedAt: nullUnix(now), ID: id})
	return affected(n, err, "disable totp")
}

// UseTOTPStep records step as the last used TOTP step and reports whether it is after
// the stored one, so of two requests with the same code only one succeeds.
func (r *Users) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	n, err := r.q.UseUserTOTPStep(ctx, sqlc.UseUserTOTPStepParams{Step: sql.NullInt64{Int64: step, Valid: true}, ID: id})
	if err != nil {
		return false, fmt.Errorf("use totp step: %w", err)
	}
	return n == 1, nil
}

// RecoveryCodes stores the hashes of unused recovery codes by prefix.
type RecoveryCodes struct {
	q *sqlc.Queries
}

// RecoveryCode is the stored hash of a recovery code.
type RecoveryCode struct {
	ID   int64
	Hash string
}

// Replace replaces the user's codes with codes, of which the prefix and hash are stored.
func (r *RecoveryCodes) Replace(ctx context.Context, userID int64, codes []credential.Token, now time.Time) error {
	if err := r.q.DeleteUserRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	for _, c := range codes {
		err := r.q.CreateRecoveryCode(ctx, sqlc.CreateRecoveryCodeParams{
			UserID: userID, Prefix: c.Prefix, CodeHash: c.Hash, CreatedAt: now.Unix(),
		})
		if err != nil {
			return fmt.Errorf("create recovery code: %w", err)
		}
	}
	return nil
}

// Count returns how many unused codes the user has.
func (r *RecoveryCodes) Count(ctx context.Context, userID int64) (int, error) {
	n, err := r.q.CountRecoveryCodes(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes: %w", err)
	}
	return int(n), nil
}

// ListByPrefix returns the user's codes with prefix, usually one.
func (r *RecoveryCodes) ListByPrefix(ctx context.Context, userID int64, prefix string) ([]RecoveryCode, error) {
	rows, err := r.q.ListRecoveryCodesByPrefix(ctx, sqlc.ListRecoveryCodesByPrefixParams{UserID: userID, Prefix: prefix})
	if err != nil {
		return nil, fmt.Errorf("list recovery codes: %w", err)
	}
	codes := make([]RecoveryCode, len(rows))
	for i, c := range rows {
		codes[i] = RecoveryCode{ID: c.ID, Hash: c.CodeHash}
	}
	return codes, nil
}

// Delete deletes a code and reports whether it was there, so it is used once.
func (r *RecoveryCodes) Delete(ctx context.Context, id int64) (bool, error) {
	n, err := r.q.DeleteRecoveryCode(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete recovery code: %w", err)
	}
	return n == 1, nil
}

// DeleteForUser deletes all codes of a user.
func (r *RecoveryCodes) DeleteForUser(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserRecoveryCodes(ctx, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}

// LoginChallenges stores sign-ins waiting for a second-factor code by token hash.
type LoginChallenges struct {
	q *sqlc.Queries
}

// Create stores a challenge.
func (r *LoginChallenges) Create(
	ctx context.Context, tokenHash string, userID int64, userAgent string, expires, now time.Time,
) error {
	err := r.q.CreateLoginChallenge(ctx, sqlc.CreateLoginChallengeParams{
		UserID:    userID,
		TokenHash: tokenHash,
		UserAgent: userAgent,
		ExpiresAt: expires.Unix(),
		CreatedAt: now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("create login challenge: %w", err)
	}
	return nil
}

// TakeAttempt counts an attempt of the challenge with tokenHash and returns it, or fails
// with errors.ErrNotFound when it has none of maxAttempts left or expired at now. The
// attempt is counted before the code is checked, so parallel requests cannot try more.
func (r *LoginChallenges) TakeAttempt(
	ctx context.Context, tokenHash string, maxAttempts int, now time.Time,
) (domain.LoginChallenge, error) {
	c, err := r.q.TakeLoginChallengeAttempt(ctx, sqlc.TakeLoginChallengeAttemptParams{
		TokenHash: tokenHash, MaxAttempts: int64(maxAttempts), Now: now.Unix(),
	})
	if err != nil {
		return domain.LoginChallenge{}, notFound(err, "login challenge")
	}
	return domain.LoginChallenge{ID: c.ID, UserID: c.UserID, UserAgent: c.UserAgent, Attempts: int(c.Attempts)}, nil
}

// Delete deletes a challenge and reports whether it was there, so it is passed once.
func (r *LoginChallenges) Delete(ctx context.Context, id int64) (bool, error) {
	n, err := r.q.DeleteLoginChallenge(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete login challenge: %w", err)
	}
	return n == 1, nil
}

// DeleteForUser deletes all challenges of a user.
func (r *LoginChallenges) DeleteForUser(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserLoginChallenges(ctx, userID); err != nil {
		return fmt.Errorf("delete login challenges: %w", err)
	}
	return nil
}

// DeleteExpired deletes challenges expired at now and returns how many.
func (r *LoginChallenges) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	n, err := r.q.DeleteExpiredLoginChallenges(ctx, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired login challenges: %w", err)
	}
	return n, nil
}

// Sessions stores browser sessions by token hash.
type Sessions struct {
	q *sqlc.Queries
}

// Create stores a session.
func (r *Sessions) Create(
	ctx context.Context, tokenHash string, userID int64, userAgent string, expires, now time.Time,
) error {
	err := r.q.CreateSession(ctx, sqlc.CreateSessionParams{
		UserID:     userID,
		TokenHash:  tokenHash,
		UserAgent:  userAgent,
		ExpiresAt:  expires.Unix(),
		LastUsedAt: now.Unix(),
		CreatedAt:  now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// Get returns the session with tokenHash, live or not.
func (r *Sessions) Get(ctx context.Context, tokenHash string) (domain.Session, error) {
	row, err := r.q.GetSession(ctx, tokenHash)
	if err != nil {
		return domain.Session{}, notFound(err, "session")
	}
	return domain.Session{
		ID:         row.ID,
		User:       domain.NewUser(row.User.ID, row.User.Login, row.User.Timezone, domain.Role(row.User.Role)),
		UserAgent:  row.UserAgent,
		CreatedAt:  time.Unix(row.CreatedAt, 0),
		ExpiresAt:  time.Unix(row.ExpiresAt, 0),
		LastUsedAt: time.Unix(row.LastUsedAt, 0),
	}, nil
}

// List returns the user's sessions, live or not, newest first, without their User.
func (r *Sessions) List(ctx context.Context, userID int64) ([]domain.Session, error) {
	rows, err := r.q.ListSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make([]domain.Session, len(rows))
	for i, s := range rows {
		out[i] = domain.Session{
			ID:         s.ID,
			UserAgent:  s.UserAgent,
			CreatedAt:  time.Unix(s.CreatedAt, 0),
			ExpiresAt:  time.Unix(s.ExpiresAt, 0),
			LastUsedAt: time.Unix(s.LastUsedAt, 0),
		}
	}
	return out, nil
}

// Touch moves the expiry of a session and records that it was used at now.
func (r *Sessions) Touch(ctx context.Context, id int64, expires, now time.Time) error {
	err := r.q.TouchSession(ctx, sqlc.TouchSessionParams{ExpiresAt: expires.Unix(), LastUsedAt: now.Unix(), ID: id})
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// Delete deletes a session.
func (r *Sessions) Delete(ctx context.Context, tokenHash string) error {
	if err := r.q.DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteByID deletes one session of a user.
func (r *Sessions) DeleteByID(ctx context.Context, userID, id int64) error {
	n, err := r.q.DeleteUserSession(ctx, sqlc.DeleteUserSessionParams{ID: id, UserID: userID})
	return affected(n, err, "delete session")
}

// DeleteForUser deletes all sessions of a user.
func (r *Sessions) DeleteForUser(ctx context.Context, userID int64) error {
	if err := r.q.DeleteUserSessions(ctx, userID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	return nil
}

// DeleteExpired deletes sessions expired at now and returns how many.
func (r *Sessions) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	n, err := r.q.DeleteExpiredSessions(ctx, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return n, nil
}

// APIKeys stores API keys by hash.
type APIKeys struct {
	q *sqlc.Queries
}

// StoredKey is an API key with its hash, which never leaves the auth service.
type StoredKey struct {
	Key  domain.APIKey
	Hash string
}

// Create stores a new key.
func (r *APIKeys) Create(
	ctx context.Context, userID int64, name string, scope domain.Scope, tok credential.Token, expires, now time.Time,
) (domain.APIKey, error) {
	k, err := r.q.CreateAPIKey(ctx, sqlc.CreateAPIKeyParams{
		UserID:    userID,
		Name:      name,
		Prefix:    tok.Prefix,
		KeyHash:   tok.Hash,
		Scope:     string(scope),
		ExpiresAt: nullUnix(expires),
		CreatedAt: now.Unix(),
	})
	if err != nil {
		return domain.APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	return apiKeyFromRow(k), nil
}

// List returns the user's keys, newest first.
func (r *APIKeys) List(ctx context.Context, userID int64) ([]domain.APIKey, error) {
	rows, err := r.q.ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	out := make([]domain.APIKey, len(rows))
	for i, k := range rows {
		out[i] = apiKeyFromRow(k)
	}
	return out, nil
}

// ByPrefix returns the key with prefix and its hash.
func (r *APIKeys) ByPrefix(ctx context.Context, prefix string) (StoredKey, error) {
	k, err := r.q.GetAPIKeyByPrefix(ctx, prefix)
	if err != nil {
		return StoredKey{}, notFound(err, "api key")
	}
	return StoredKey{Key: apiKeyFromRow(k), Hash: k.KeyHash}, nil
}

// Revoke revokes a live key of the user.
func (r *APIKeys) Revoke(ctx context.Context, userID, id int64, now time.Time) error {
	n, err := r.q.RevokeAPIKey(ctx, sqlc.RevokeAPIKeyParams{RevokedAt: nullUnix(now), ID: id, UserID: userID})
	return affected(n, err, "revoke api key")
}

// Touch records that the key was used at now.
func (r *APIKeys) Touch(ctx context.Context, id int64, now time.Time) error {
	if err := r.q.TouchAPIKey(ctx, sqlc.TouchAPIKeyParams{LastUsedAt: nullUnix(now), ID: id}); err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

func apiKeyFromRow(k sqlc.ApiKey) domain.APIKey {
	key := domain.APIKey{
		ID:        k.ID,
		UserID:    k.UserID,
		Name:      k.Name,
		Prefix:    k.Prefix,
		Scope:     domain.Scope(k.Scope),
		Revoked:   k.RevokedAt.Valid,
		CreatedAt: time.Unix(k.CreatedAt, 0),
	}
	if k.ExpiresAt.Valid {
		key.ExpiresAt = time.Unix(k.ExpiresAt.Int64, 0)
	}
	if k.LastUsedAt.Valid {
		key.LastUsedAt = time.Unix(k.LastUsedAt.Int64, 0)
	}
	return key
}
