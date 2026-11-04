-- name: UpsertSyncedRate :exec
-- keeps manual rates and the updated_at of unchanged ones
INSERT INTO exchange_rates (source, currency, date, rate, is_manual, created_at)
VALUES (?, ?, ?, ?, 0, ?)
ON CONFLICT (source, currency, date) DO UPDATE SET rate = excluded.rate, updated_at = excluded.created_at
WHERE exchange_rates.is_manual = 0 AND exchange_rates.rate <> excluded.rate;

-- name: UpsertRate :exec
INSERT INTO exchange_rates (source, currency, date, rate, is_manual, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (source, currency, date) DO UPDATE SET
    rate = excluded.rate, is_manual = excluded.is_manual, updated_at = excluded.created_at;

-- name: GetRateBounds :one
SELECT CAST(COALESCE(MIN(date), '') AS TEXT) AS first_date, CAST(COALESCE(MAX(date), '') AS TEXT) AS last_date
FROM exchange_rates
WHERE source = ? AND currency = ?;

-- name: ListLatestRates :many
SELECT r.*
FROM exchange_rates AS r
JOIN (
    SELECT x.currency, MAX(x.date) AS date FROM exchange_rates AS x WHERE x.source = @source GROUP BY x.currency
) AS l ON l.currency = r.currency AND l.date = r.date
WHERE r.source = @source
ORDER BY r.currency;

-- name: ListRateUsers :many
-- settings of every user with the first date of their operations and the currencies of
-- their accounts, for the rate sync to pick the ones it needs
SELECT
    s.user_id,
    s.rate_source,
    s.primary_currency,
    CAST(COALESCE((SELECT MIN(o.date) FROM operations AS o WHERE o.user_id = s.user_id), '') AS TEXT) AS first_date,
    CAST(COALESCE((SELECT group_concat(DISTINCT a.currency) FROM accounts AS a WHERE a.user_id = s.user_id), '') AS TEXT) AS currencies
FROM user_settings AS s;

-- name: ListRates :many
SELECT * FROM exchange_rates
WHERE source = ? AND currency = ? AND date >= sqlc.arg(from_date) AND date <= sqlc.arg(to_date)
ORDER BY date;
