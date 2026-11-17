-- +goose Up
DROP INDEX operations_user_date_idx;
-- the order of ListOperations: newest first by date and time, parts of a split together
CREATE INDEX operations_user_order_idx ON operations (
    user_id, date DESC, coalesce(time, '') DESC, coalesce(origin_id, id) DESC, id DESC
);

-- +goose Down
DROP INDEX operations_user_order_idx;
CREATE INDEX operations_user_date_idx ON operations (user_id, date DESC, id DESC);
