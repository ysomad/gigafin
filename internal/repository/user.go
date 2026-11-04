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

// Credentials is a user with the password hash, which never leaves the auth service.
type Credentials struct {
	User         domain.User
	PasswordHash string
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
	return Credentials{
		User: domain.NewUser(u.ID, u.Login, u.Timezone, domain.Role(u.Role)), PasswordHash: u.PasswordHash,
	}, nil
}

// CredentialsByLogin returns a user by login with the password hash.
func (r *Users) CredentialsByLogin(ctx context.Context, login string) (Credentials, error) {
	u, err := r.q.GetUserByLogin(ctx, login)
	if err != nil {
		return Credentials{}, notFound(err, "user")
	}
	return Credentials{
		User: domain.NewUser(u.ID, u.Login, u.Timezone, domain.Role(u.Role)), PasswordHash: u.PasswordHash,
	}, nil
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
