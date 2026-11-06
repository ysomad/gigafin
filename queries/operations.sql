-- name: CreateOperation :one
INSERT INTO operations (
    user_id, type, account_id, to_account_id, category_id, origin_id, amount, date, time, payee,
    source_payee, note, mcc, import_id, import_hash, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateOperation :one
UPDATE operations
SET type = ?, account_id = ?, to_account_id = ?, category_id = ?, origin_id = ?, amount = ?, date = ?,
    time = ?, payee = ?, note = ?, updated_at = ?
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: GetOperation :one
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.id = ? AND o.user_id = ?;

-- name: ListOperationsByPayee :many
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.payee = sqlc.arg(payee)
  AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
ORDER BY o.date DESC, o.id DESC;

-- name: DeleteOperation :execrows
DELETE FROM operations WHERE id = ? AND user_id = ?;

-- name: ListOperations :many
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id)
  AND (o.account_id = sqlc.narg(account_id) OR o.to_account_id = sqlc.narg(account_id) OR sqlc.narg(account_id) IS NULL)
  AND (o.type = sqlc.narg(type) OR sqlc.narg(type) IS NULL)
  AND (o.date >= sqlc.narg(date_from) OR sqlc.narg(date_from) IS NULL)
  AND (o.date <= sqlc.narg(date_to) OR sqlc.narg(date_to) IS NULL)
  AND (o.category_id = sqlc.narg(category_id)
       OR o.category_id IN (SELECT c.id FROM categories AS c WHERE c.parent_id = sqlc.narg(category_id))
       OR sqlc.narg(category_id) IS NULL)
  AND (o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg(query))
       OR sqlc.narg(query) IS NULL)
  -- amount bounds are JSON objects of minor units by currency, see Operations.List
  AND (abs(o.amount) >= (sqlc.narg(amount_min) ->> a.currency) OR sqlc.narg(amount_min) IS NULL)
  AND (abs(o.amount) <= (sqlc.narg(amount_max) ->> a.currency) OR sqlc.narg(amount_max) IS NULL)
  -- tags are names wrapped in commas, ",a,b,", as names have none; an operation
  -- matches any of them
  AND (o.id IN (SELECT ot.operation_id FROM operation_tags AS ot JOIN tags AS t ON t.id = ot.tag_id
                WHERE t.user_id = sqlc.arg(user_id) AND instr(sqlc.narg(tags), ',' || t.name || ',') > 0)
       OR sqlc.narg(tags) IS NULL)
  AND (o.date < sqlc.narg(before_date)
       OR (o.date = sqlc.narg(before_date) AND o.id < sqlc.narg(before_id))
       OR sqlc.narg(before_date) IS NULL)
ORDER BY o.date DESC, o.id DESC
LIMIT sqlc.arg(lim);

-- name: SumFilteredOperations :many
-- totals of the operations ListOperations matches, by type and currency; keep the
-- conditions the same
SELECT o.type, a.currency, CAST(SUM(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id)
  AND (o.account_id = sqlc.narg(account_id) OR o.to_account_id = sqlc.narg(account_id) OR sqlc.narg(account_id) IS NULL)
  AND (o.type = sqlc.narg(type) OR sqlc.narg(type) IS NULL)
  AND (o.date >= sqlc.narg(date_from) OR sqlc.narg(date_from) IS NULL)
  AND (o.date <= sqlc.narg(date_to) OR sqlc.narg(date_to) IS NULL)
  AND (o.category_id = sqlc.narg(category_id)
       OR o.category_id IN (SELECT c.id FROM categories AS c WHERE c.parent_id = sqlc.narg(category_id))
       OR sqlc.narg(category_id) IS NULL)
  AND (o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg(query))
       OR sqlc.narg(query) IS NULL)
  -- amount bounds are JSON objects of minor units by currency, see Operations.List
  AND (abs(o.amount) >= (sqlc.narg(amount_min) ->> a.currency) OR sqlc.narg(amount_min) IS NULL)
  AND (abs(o.amount) <= (sqlc.narg(amount_max) ->> a.currency) OR sqlc.narg(amount_max) IS NULL)
  -- tags are names wrapped in commas, ",a,b,", as names have none; an operation
  -- matches any of them
  AND (o.id IN (SELECT ot.operation_id FROM operation_tags AS ot JOIN tags AS t ON t.id = ot.tag_id
                WHERE t.user_id = sqlc.arg(user_id) AND instr(sqlc.narg(tags), ',' || t.name || ',') > 0)
       OR sqlc.narg(tags) IS NULL)
GROUP BY o.type, a.currency;

-- name: DeleteImportOperations :execrows
DELETE FROM operations WHERE import_id = ? AND user_id = ?;

-- name: DeleteUserOperations :exec
DELETE FROM operations WHERE user_id = ?;

-- name: SumOperationsByCategory :many
SELECT CAST(o.category_id AS INTEGER) AS category_id, a.currency, CAST(SUM(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
  AND o.category_id IS NOT NULL
GROUP BY o.category_id, a.currency;

-- name: SumOperationsByMonth :many
SELECT CAST(substr(o.date, 1, 7) AS TEXT) AS month, o.type, a.currency, CAST(SUM(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
GROUP BY month, o.type, a.currency
ORDER BY month;

-- name: SumOperationsByCategoryMonth :many
SELECT CAST(substr(o.date, 1, 7) AS TEXT) AS month, CAST(o.category_id AS INTEGER) AS category_id, a.currency,
    CAST(SUM(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
  AND o.category_id IS NOT NULL
GROUP BY month, o.category_id, a.currency;

-- name: SumOperationsByPayee :many
SELECT CAST(o.payee AS TEXT) AS payee, o.type, a.currency, CAST(SUM(o.amount) AS INTEGER) AS total,
    COUNT(*) AS ops
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
  AND o.payee IS NOT NULL
GROUP BY o.payee, o.type, a.currency;

-- name: SumBalanceChangesByMonth :many
-- balance changes of each account per month up to date_to, with the signs of the
-- account_balances view
SELECT CAST(c.month AS TEXT) AS month, c.account_id, CAST(c.currency AS TEXT) AS currency,
    CAST(SUM(c.delta) AS INTEGER) AS total
FROM (
    SELECT substr(o.date, 1, 7) AS month, a.id AS account_id, a.currency AS currency,
        CASE WHEN o.type IN ('expense', 'transfer') THEN -o.amount ELSE o.amount END AS delta
    FROM operations AS o
    JOIN accounts AS a ON a.id = o.account_id
    WHERE o.user_id = sqlc.arg(user_id) AND o.date <= sqlc.arg(date_to)
    UNION ALL
    SELECT substr(o.date, 1, 7), a.id, a.currency, o.amount
    FROM operations AS o
    JOIN accounts AS a ON a.id = o.to_account_id
    WHERE o.user_id = sqlc.arg(user_id) AND o.date <= sqlc.arg(date_to)
) AS c
GROUP BY c.month, c.account_id, c.currency
ORDER BY c.month;

-- name: OperationDateRange :one
SELECT CAST(COALESCE(MIN(date), '') AS TEXT) AS first_date, CAST(COALESCE(MAX(date), '') AS TEXT) AS last_date
FROM operations
WHERE user_id = sqlc.arg(user_id);
