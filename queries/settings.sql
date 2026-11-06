-- name: GetUserSettings :one
SELECT * FROM user_settings WHERE user_id = ?;

-- name: UpsertUserSettings :exec
INSERT INTO user_settings (
    user_id, show_minor_units, primary_currency, rate_source, language, auto_category_rules, created_at
)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
    show_minor_units = excluded.show_minor_units,
    primary_currency = excluded.primary_currency,
    rate_source = excluded.rate_source,
    language = excluded.language,
    auto_category_rules = excluded.auto_category_rules,
    updated_at = excluded.created_at;
