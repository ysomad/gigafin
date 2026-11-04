-- name: CreateImport :one
INSERT INTO imports (
    user_id, source, filename, status, rows_total, rows_new, rows_dup, rows_skip, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetImport :one
SELECT * FROM imports WHERE id = ? AND user_id = ?;

-- name: ListImports :many
SELECT * FROM imports WHERE user_id = ? ORDER BY id DESC LIMIT ?;

-- name: UpdateImport :execrows
UPDATE imports
SET status = ?, rows_total = ?, rows_new = ?, rows_dup = ?, rows_skip = ?
WHERE id = ? AND user_id = ?;

-- name: DeleteImport :execrows
DELETE FROM imports WHERE id = ? AND user_id = ?;

-- name: CreateImportRow :one
INSERT INTO import_rows (import_id, line, payload, import_hash, status)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: ListImportRows :many
SELECT * FROM import_rows WHERE import_id = ? ORDER BY line;

-- name: SetImportRowPair :exec
UPDATE import_rows SET pair_row_id = ? WHERE id = ?;

-- name: SetImportRowOperation :exec
UPDATE import_rows SET operation_id = ? WHERE id = ?;

-- name: ListAccountMap :many
SELECT m.source_name, m.currency AS map_currency, sqlc.embed(a)
FROM import_account_map AS m
JOIN accounts AS a ON a.id = m.account_id
WHERE m.user_id = ? AND m.source = ?;

-- name: UpsertAccountMap :exec
INSERT INTO import_account_map (user_id, source, source_name, currency, account_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, source, source_name, currency) DO UPDATE SET account_id = excluded.account_id;

-- name: ListCategoryMap :many
SELECT m.source_name, m.kind AS map_kind, sqlc.embed(c)
FROM import_category_map AS m
JOIN categories AS c ON c.id = m.category_id
WHERE m.user_id = ? AND m.source = ?;

-- name: UpsertCategoryMap :exec
INSERT INTO import_category_map (user_id, source, source_name, kind, category_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, source, source_name, kind) DO UPDATE SET category_id = excluded.category_id;

-- name: ListImportedHashes :many
SELECT r.import_hash
FROM import_rows AS r
JOIN imports AS b ON b.id = r.import_id
WHERE b.user_id = sqlc.arg(user_id)
  AND b.status = sqlc.arg(import_status)
  AND r.status = sqlc.arg(row_status)
  AND r.import_hash IN (sqlc.slice(hashes));

-- name: DeleteUserImports :exec
DELETE FROM imports WHERE user_id = ?;

-- name: DeleteUserAccountMaps :exec
DELETE FROM import_account_map WHERE user_id = ?;

-- name: DeleteUserCategoryMaps :exec
DELETE FROM import_category_map WHERE user_id = ?;
