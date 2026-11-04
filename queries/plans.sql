-- name: UpsertPlan :exec
INSERT INTO plans (user_id, category_id, month, currency, amount, is_repeating)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id, month, currency) DO UPDATE SET amount = excluded.amount,
    is_repeating = excluded.is_repeating;

-- name: DeletePlan :exec
DELETE FROM plans WHERE user_id = ? AND category_id = ? AND month = ? AND currency = ?;

-- name: ListPlans :many
SELECT * FROM plans WHERE user_id = ? AND month = ? ORDER BY category_id, currency;

-- name: ListRepeatingPlans :many
SELECT * FROM plans WHERE user_id = ? AND is_repeating = 1 AND month < ? ORDER BY month;

-- name: InsertPlanIfAbsent :execrows
INSERT INTO plans (user_id, category_id, month, currency, amount, is_repeating)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id, month, currency) DO NOTHING;

-- name: InsertPlanCarry :execrows
INSERT INTO plan_carries (user_id, month, created_at)
VALUES (?, ?, ?)
ON CONFLICT (user_id, month) DO NOTHING;

-- name: DeleteUserPlans :exec
DELETE FROM plans WHERE user_id = ?;

-- name: DeleteUserPlanCarries :exec
DELETE FROM plan_carries WHERE user_id = ?;
