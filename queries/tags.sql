-- name: UpsertTag :one
INSERT INTO tags (user_id, name, created_at) VALUES (?, ?, ?)
ON CONFLICT (user_id, name) DO UPDATE SET name = excluded.name
RETURNING id;

-- name: ListTags :many
SELECT name FROM tags WHERE user_id = ? ORDER BY name;

-- name: DeleteOperationTags :many
DELETE FROM operation_tags WHERE operation_id = ?
RETURNING tag_id;

-- name: CreateOperationTag :exec
INSERT INTO operation_tags (operation_id, tag_id) VALUES (?, ?);

-- name: ListOperationTags :many
SELECT ot.operation_id, t.name
FROM operation_tags AS ot
JOIN tags AS t ON t.id = ot.tag_id
WHERE ot.operation_id IN (sqlc.slice(operation_ids))
ORDER BY ot.operation_id, t.name;

-- name: DeleteUnusedTags :exec
DELETE FROM tags WHERE user_id = ? AND id NOT IN (SELECT ot.tag_id FROM operation_tags AS ot);

-- name: DeleteUnusedTagsByID :exec
DELETE FROM tags
WHERE id IN (sqlc.slice(ids)) AND NOT EXISTS (SELECT 1 FROM operation_tags AS ot WHERE ot.tag_id = tags.id);

-- name: DeleteUserTags :exec
DELETE FROM tags WHERE user_id = ?;
