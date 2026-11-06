-- name: CreateRule :one
INSERT INTO rules (user_id, stage, match, conditions, actions, origin, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRule :one
UPDATE rules
SET stage = ?, match = ?, conditions = ?, actions = ?, origin = ?, updated_at = ?
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: GetRule :one
SELECT * FROM rules WHERE id = ? AND user_id = ?;

-- name: ListRules :many
SELECT * FROM rules WHERE user_id = ? ORDER BY id;

-- name: DeleteRule :execrows
DELETE FROM rules WHERE id = ? AND user_id = ?;

-- name: DeleteUserRules :exec
DELETE FROM rules WHERE user_id = ?;
