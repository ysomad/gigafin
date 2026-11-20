-- name: CreateOperation :one
INSERT INTO operations (
    user_id, type, account_id, to_account_id, category_id, origin_id, amount, date, time, payee,
    source_payee, note, mcc, import_id, import_hash, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateOperation :one
UPDATE operations
SET
    type = ?, account_id = ?, to_account_id = ?, category_id = ?, origin_id = ?, amount = ?, date = ?,
    time = ?, payee = ?, note = ?, updated_at = ?
WHERE id = ? AND user_id = ?
RETURNING *;

-- name: SetOperationOrigin :exec
UPDATE operations SET origin_id = ? WHERE id = ? AND user_id = ?;

-- name: GetOperation :one
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.id = ? AND o.user_id = ?;

-- name: ListOperationsByIDs :many
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.id IN (sqlc.slice(ids));

-- name: ListOperationsByPayees :many
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.payee IN (sqlc.slice(payees))
ORDER BY o.date DESC, coalesce(o.time, '') DESC, coalesce(o.origin_id, o.id) DESC, o.id DESC;

-- name: DeleteOperation :execrows
DELETE FROM operations WHERE id = ? AND user_id = ?;

-- name: ListOperations :many
SELECT sqlc.embed(o), a.currency
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE
    o.user_id = sqlc.arg(user_id)
    AND (
        o.account_id = sqlc.narg(account_id) OR o.to_account_id = sqlc.narg(account_id) OR sqlc.narg(account_id) IS NULL
    )
    AND (o.type = sqlc.narg(type) OR sqlc.narg(type) IS NULL)
    AND (o.date >= sqlc.narg(date_from) OR sqlc.narg(date_from) IS NULL)
    AND (o.date <= sqlc.narg(date_to) OR sqlc.narg(date_to) IS NULL)
    AND (
        o.category_id = sqlc.narg(category_id)
        OR o.category_id IN (SELECT c.id FROM categories AS c WHERE c.parent_id = sqlc.narg(category_id))
        OR sqlc.narg(category_id) IS NULL
    )
    AND (
        (o.type IN ('expense', 'income') AND o.category_id IS NULL)
        OR NOT cast(sqlc.arg(uncategorized) AS INTEGER)
    )
    AND (
        o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg (query))
        OR sqlc.narg (query) IS NULL
    )
    -- the numbers of the query match the text as the query does, or the absolute amount
    -- within bounds as amount_min and amount_max below
    AND (
        o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg (number_query))
        OR abs(o.amount) BETWEEN (sqlc.narg(number_min) ->> a.currency) AND (sqlc.narg(number_max) ->> a.currency)
        OR sqlc.narg (number_query) IS NULL
    )
    -- amount bounds are JSON objects of minor units by currency, see Operations.List
    AND (abs(o.amount) >= (sqlc.narg(amount_min) ->> a.currency) OR sqlc.narg(amount_min) IS NULL)
    AND (abs(o.amount) <= (sqlc.narg(amount_max) ->> a.currency) OR sqlc.narg(amount_max) IS NULL)
    -- tags are names wrapped in commas, ",a,b,", as names have none; an operation
    -- matches any of them
    AND (o.id IN (
        SELECT ot.operation_id FROM operation_tags AS ot JOIN tags AS t ON t.id = ot.tag_id
        WHERE t.user_id = sqlc.arg(user_id) AND instr(sqlc.narg(tags), ',' || t.name || ',') > 0
    )
    OR sqlc.narg(tags) IS NULL)
    -- seeks past the cursor in the order below, see pagination.Cursor
    AND (
        (o.date, coalesce(o.time, ''), coalesce(o.origin_id, o.id), o.id)
        < (
            cast(sqlc.narg(before_date) AS TEXT), cast(sqlc.narg(before_time) AS TEXT),
            cast(sqlc.narg(before_group) AS INTEGER), cast(sqlc.narg(before_id) AS INTEGER)
        )
        OR sqlc.narg(before_date) IS NULL
    )
ORDER BY o.date DESC, coalesce(o.time, '') DESC, coalesce(o.origin_id, o.id) DESC, o.id DESC
LIMIT sqlc.arg(lim);

-- name: SumFilteredOperations :many
-- totals of the operations ListOperations matches, by type and currency; keep the
-- conditions the same
SELECT o.type, a.currency, cast(sum(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE
    o.user_id = sqlc.arg(user_id)
    AND (
        o.account_id = sqlc.narg(account_id) OR o.to_account_id = sqlc.narg(account_id) OR sqlc.narg(account_id) IS NULL
    )
    AND (o.type = sqlc.narg(type) OR sqlc.narg(type) IS NULL)
    AND (o.date >= sqlc.narg(date_from) OR sqlc.narg(date_from) IS NULL)
    AND (o.date <= sqlc.narg(date_to) OR sqlc.narg(date_to) IS NULL)
    AND (
        o.category_id = sqlc.narg(category_id)
        OR o.category_id IN (SELECT c.id FROM categories AS c WHERE c.parent_id = sqlc.narg(category_id))
        OR sqlc.narg(category_id) IS NULL
    )
    AND (
        (o.type IN ('expense', 'income') AND o.category_id IS NULL)
        OR NOT cast(sqlc.arg(uncategorized) AS INTEGER)
    )
    AND (
        o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg (query))
        OR sqlc.narg (query) IS NULL
    )
    -- the numbers of the query match the text as the query does, or the absolute amount
    -- within bounds as amount_min and amount_max below
    AND (
        o.id IN (SELECT f.rowid FROM operations_fts AS f WHERE f.body MATCH sqlc.narg (number_query))
        OR abs(o.amount) BETWEEN (sqlc.narg(number_min) ->> a.currency) AND (sqlc.narg(number_max) ->> a.currency)
        OR sqlc.narg (number_query) IS NULL
    )
    -- amount bounds are JSON objects of minor units by currency, see Operations.List
    AND (abs(o.amount) >= (sqlc.narg(amount_min) ->> a.currency) OR sqlc.narg(amount_min) IS NULL)
    AND (abs(o.amount) <= (sqlc.narg(amount_max) ->> a.currency) OR sqlc.narg(amount_max) IS NULL)
    -- tags are names wrapped in commas, ",a,b,", as names have none; an operation
    -- matches any of them
    AND (o.id IN (
        SELECT ot.operation_id FROM operation_tags AS ot JOIN tags AS t ON t.id = ot.tag_id
        WHERE t.user_id = sqlc.arg(user_id) AND instr(sqlc.narg(tags), ',' || t.name || ',') > 0
    )
    OR sqlc.narg(tags) IS NULL)
GROUP BY o.type, a.currency;

-- name: DeleteImportOperations :execrows
DELETE FROM operations WHERE import_id = ? AND user_id = ?;

-- name: DeleteUserOperations :exec
DELETE FROM operations WHERE user_id = ?;

-- name: SumOperationsByCategory :many
SELECT cast(o.category_id AS INTEGER) AS category_id, a.currency, cast(sum(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE
    o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
    AND o.category_id IS NOT NULL
GROUP BY o.category_id, a.currency;

-- name: SumOperationsByMonth :many
SELECT cast(substr(o.date, 1, 7) AS TEXT) AS month, o.type, a.currency, cast(sum(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
GROUP BY month, o.type, a.currency
ORDER BY month;

-- name: SumOperationsByCategoryMonth :many
SELECT
    cast(substr(o.date, 1, 7) AS TEXT) AS month, cast(o.category_id AS INTEGER) AS category_id, a.currency,
    cast(sum(o.amount) AS INTEGER) AS total
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE
    o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
    AND o.category_id IS NOT NULL
GROUP BY month, o.category_id, a.currency;

-- name: SumOperationsByPayee :many
SELECT
    cast(o.payee AS TEXT) AS payee, o.type, a.currency, cast(sum(o.amount) AS INTEGER) AS total,
    count(*) AS ops
FROM operations AS o
JOIN accounts AS a ON a.id = o.account_id
WHERE
    o.user_id = sqlc.arg(user_id) AND o.date >= sqlc.arg(date_from) AND o.date <= sqlc.arg(date_to)
    AND o.payee IS NOT NULL
GROUP BY o.payee, o.type, a.currency;

-- name: SumBalanceChangesByMonth :many
-- balance changes of each account per month up to date_to, with the signs of the
-- account_balances view
SELECT
    cast(c.month AS TEXT) AS month, c.account_id, cast(c.currency AS TEXT) AS currency,
    cast(sum(c.delta) AS INTEGER) AS total
FROM (
    SELECT
        substr(o.date, 1, 7) AS month, a.id AS account_id, a.currency,
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
SELECT cast(coalesce(min(date), '') AS TEXT) AS first_date, cast(coalesce(max(date), '') AS TEXT) AS last_date
FROM operations
WHERE user_id = sqlc.arg(user_id);
