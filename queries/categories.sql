-- name: CreateCategory :one
INSERT INTO categories (user_id, parent_id, kind, name, created_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = ? AND user_id = ?;

-- name: GetRootCategoryByName :one
SELECT * FROM categories WHERE user_id = ? AND kind = ? AND name = ? AND parent_id IS NULL;

-- name: GetChildCategoryByName :one
SELECT * FROM categories WHERE parent_id = ? AND name = ?;

-- name: ListCategoriesByID :many
SELECT * FROM categories WHERE user_id = sqlc.arg(user_id) AND id IN (sqlc.slice(ids));

-- name: ListCategories :many
SELECT * FROM categories WHERE user_id = ? ORDER BY kind, name;

-- name: UpdateCategory :execrows
UPDATE categories SET name = ?, parent_id = ? WHERE id = ? AND user_id = ?;

-- name: SetCategoryArchived :execrows
UPDATE categories SET archived_at = ? WHERE id = ? AND user_id = ?;

-- name: CountCategoryChildren :one
SELECT COUNT(*) FROM categories WHERE parent_id = ?;

-- name: CountCategoryOperations :one
SELECT COUNT(*) FROM operations WHERE category_id = ?;

-- name: DeleteCategory :execrows
DELETE FROM categories WHERE id = ? AND user_id = ?;

-- name: DeleteUserSubcategories :exec
DELETE FROM categories WHERE user_id = ? AND parent_id IS NOT NULL;

-- name: DeleteUserCategories :exec
DELETE FROM categories WHERE user_id = ?;
