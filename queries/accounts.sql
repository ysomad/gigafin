-- name: CreateAccount :one
INSERT INTO accounts (user_id, name, currency, kind, credit_limit, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAccount :one
SELECT sqlc.embed(accounts), b.balance
FROM accounts
JOIN account_balances AS b ON b.account_id = accounts.id
WHERE accounts.id = ? AND accounts.user_id = ?;

-- name: ListAccounts :many
SELECT sqlc.embed(accounts), b.balance
FROM accounts
JOIN account_balances AS b ON b.account_id = accounts.id
WHERE accounts.user_id = ?
ORDER BY accounts.archived_at IS NOT NULL, accounts.name;

-- name: UpdateAccount :execrows
UPDATE accounts SET name = ?, kind = ?, credit_limit = ? WHERE id = ? AND user_id = ?;

-- name: SetAccountArchived :execrows
UPDATE accounts SET archived_at = ? WHERE id = ? AND user_id = ?;

-- name: CountAccountOperations :one
SELECT COUNT(*) FROM operations WHERE account_id = sqlc.arg(account_id) OR to_account_id = sqlc.arg(account_id);

-- name: DeleteAccount :execrows
DELETE FROM accounts WHERE id = ? AND user_id = ?;

-- name: DeleteUserAccounts :exec
DELETE FROM accounts WHERE user_id = ?;

-- name: GetAccountBalanceAt :one
-- balance at the end of date, see the account_balances view
SELECT CAST(COALESCE((
    SELECT SUM(CASE WHEN o.type IN ('expense', 'transfer') THEN -o.amount ELSE o.amount END)
    FROM operations AS o
    WHERE o.account_id = sqlc.arg(account_id) AND o.date <= sqlc.arg(date)
), 0) + COALESCE((
    SELECT SUM(o.amount) FROM operations AS o
    WHERE o.to_account_id = sqlc.arg(account_id) AND o.date <= sqlc.arg(date)
), 0) AS INTEGER) AS balance;
