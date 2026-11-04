-- name: CreateUser :one
INSERT INTO users (login, password_hash, timezone, role, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY id;

-- name: GetUserByLogin :one
SELECT * FROM users WHERE login = ?;

-- name: UpdateUserPassword :execrows
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserRoleByLogin :execrows
UPDATE users SET role = ?, updated_at = ? WHERE login = ?;

-- name: UpdateUserTimezone :execrows
UPDATE users SET timezone = ?, updated_at = ? WHERE id = ?;

-- name: CreateSession :exec
INSERT INTO sessions (user_id, token_hash, user_agent, expires_at, last_used_at, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT sqlc.embed(users), sessions.id, sessions.user_agent, sessions.expires_at, sessions.last_used_at,
    sessions.created_at
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ?;

-- name: ListSessions :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY id DESC;

-- name: TouchSession :exec
UPDATE sessions SET expires_at = ?, last_used_at = ? WHERE id = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteUserSession :execrows
DELETE FROM sessions WHERE id = ? AND user_id = ?;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ?;

-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, prefix, key_hash, scope, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAPIKeys :many
SELECT * FROM api_keys WHERE user_id = ? ORDER BY id DESC;

-- name: GetAPIKeyByPrefix :one
SELECT * FROM api_keys WHERE prefix = ?;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = ? WHERE id = ?;
