-- name: GetBankConnection :one
SELECT * FROM bank_connections WHERE user_id = ? AND bank = ?;

-- name: UpsertBankConnection :exec
INSERT INTO bank_connections (user_id, bank, login, status, secret, last_sync_at, last_error, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, bank) DO UPDATE SET
    login = excluded.login,
    status = excluded.status,
    secret = excluded.secret,
    last_sync_at = excluded.last_sync_at,
    last_error = excluded.last_error,
    updated_at = excluded.created_at;

-- name: DeleteBankConnection :exec
DELETE FROM bank_connections WHERE user_id = ? AND bank = ?;

-- name: ListBankAccounts :many
SELECT * FROM bank_accounts WHERE user_id = ? ORDER BY bank, name, id;

-- name: UpsertBankAccount :exec
INSERT INTO bank_accounts (user_id, bank, external_id, name, currency, kind, balance, credit_limit, balance_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, bank, external_id) DO UPDATE SET
    name = excluded.name,
    currency = excluded.currency,
    kind = excluded.kind,
    balance = excluded.balance,
    credit_limit = excluded.credit_limit,
    balance_at = excluded.balance_at,
    updated_at = excluded.created_at;

-- name: LinkBankAccount :execrows
UPDATE bank_accounts SET account_id = ?, updated_at = ?
WHERE user_id = ? AND id = ? AND account_id IS NULL;

-- name: SetBankAccountSyncedUntil :exec
UPDATE bank_accounts SET synced_until = ?, updated_at = ? WHERE user_id = ? AND id = ?;

-- name: DeleteBankAccounts :exec
DELETE FROM bank_accounts WHERE user_id = ?;

-- name: DeleteBankConnections :exec
DELETE FROM bank_connections WHERE user_id = ?;
