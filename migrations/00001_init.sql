-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    login         TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    timezone      TEXT    NOT NULL,
    role          TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER
) STRICT;

CREATE TABLE user_settings (
    user_id          INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    show_minor_units INTEGER NOT NULL,
    primary_currency TEXT    NOT NULL,
    rate_source      TEXT    NOT NULL,
    language         TEXT    NOT NULL,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER
) STRICT;

CREATE TABLE sessions (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   TEXT    NOT NULL,
    user_agent   TEXT    NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_token_hash_idx ON sessions (token_hash);

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,
    key_hash     TEXT    NOT NULL,
    scope        TEXT    NOT NULL,
    expires_at   INTEGER,
    last_used_at INTEGER,
    revoked_at   INTEGER,
    created_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);
CREATE INDEX api_keys_prefix_idx ON api_keys (prefix);

CREATE TABLE accounts (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    currency    TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    credit_limit INTEGER,
    archived_at INTEGER,
    created_at  INTEGER NOT NULL,
    UNIQUE (user_id, name)
) STRICT;

CREATE TABLE categories (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_id   INTEGER REFERENCES categories (id) ON DELETE RESTRICT,
    kind        TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    archived_at INTEGER,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX categories_cat1_name_idx ON categories (user_id, kind, name) WHERE parent_id IS NULL;
CREATE UNIQUE INDEX categories_cat2_name_idx ON categories (parent_id, name) WHERE parent_id IS NOT NULL;

CREATE TABLE imports (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source     TEXT    NOT NULL,
    filename   TEXT    NOT NULL,
    status     TEXT    NOT NULL,
    rows_total INTEGER NOT NULL,
    rows_new   INTEGER NOT NULL,
    rows_dup   INTEGER NOT NULL,
    rows_skip  INTEGER NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX imports_user_id_idx ON imports (user_id, id);

CREATE TABLE operations (
    id              INTEGER PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type            TEXT    NOT NULL,
    account_id      INTEGER NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    to_account_id   INTEGER REFERENCES accounts (id) ON DELETE RESTRICT,
    category_id     INTEGER REFERENCES categories (id) ON DELETE RESTRICT,
    origin_id       INTEGER,
    amount          INTEGER NOT NULL,
    date            TEXT    NOT NULL,
    time            TEXT,
    payee           TEXT,
    source_payee    TEXT,
    note            TEXT,
    mcc             TEXT,
    import_id       INTEGER REFERENCES imports (id) ON DELETE SET NULL,
    import_hash     TEXT,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER,
    UNIQUE (user_id, import_hash)
) STRICT;

CREATE INDEX operations_user_date_idx ON operations (user_id, date DESC, id DESC);
CREATE INDEX operations_account_date_idx ON operations (account_id, date);
CREATE INDEX operations_to_account_date_idx ON operations (to_account_id, date) WHERE to_account_id IS NOT NULL;
CREATE INDEX operations_import_idx ON operations (import_id) WHERE import_id IS NOT NULL;
CREATE INDEX operations_category_idx ON operations (category_id) WHERE category_id IS NOT NULL;

CREATE VIRTUAL TABLE operations_fts USING fts5(
    body, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 2'
);

-- +goose StatementBegin
CREATE TRIGGER operations_fts_insert AFTER INSERT ON operations BEGIN
    INSERT INTO operations_fts (rowid, body)
    VALUES (new.id, replace(replace(coalesce(new.payee, '') || ' ' || coalesce(new.note, ''), 'ё', 'е'), 'Ё', 'Е'));
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER operations_fts_delete AFTER DELETE ON operations BEGIN
    DELETE FROM operations_fts WHERE rowid = old.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER operations_fts_update AFTER UPDATE OF payee, note ON operations BEGIN
    DELETE FROM operations_fts WHERE rowid = old.id;
    INSERT INTO operations_fts (rowid, body)
    VALUES (new.id, replace(replace(coalesce(new.payee, '') || ' ' || coalesce(new.note, ''), 'ё', 'е'), 'Ё', 'Е'));
END;
-- +goose StatementEnd

CREATE TABLE plans (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category_id  INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    month        TEXT    NOT NULL,
    currency     TEXT    NOT NULL,
    amount       INTEGER NOT NULL,
    is_repeating INTEGER NOT NULL,
    UNIQUE (category_id, month, currency)
) STRICT;

CREATE INDEX plans_user_month_idx ON plans (user_id, month);

CREATE TABLE plan_carries (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    month      TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, month)
) STRICT;

CREATE TABLE tags (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (user_id, name)
) STRICT;

CREATE TABLE operation_tags (
    operation_id INTEGER NOT NULL REFERENCES operations (id) ON DELETE CASCADE,
    tag_id       INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (operation_id, tag_id)
) STRICT;

CREATE INDEX operation_tags_tag_idx ON operation_tags (tag_id);

CREATE TABLE import_rows (
    id           INTEGER PRIMARY KEY,
    import_id    INTEGER NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    line         INTEGER NOT NULL,
    payload      TEXT    NOT NULL,
    import_hash  TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    pair_row_id  INTEGER REFERENCES import_rows (id) ON DELETE SET NULL,
    operation_id INTEGER REFERENCES operations (id) ON DELETE SET NULL
) STRICT;

CREATE INDEX import_rows_import_idx ON import_rows (import_id, line);
CREATE INDEX import_rows_hash_idx ON import_rows (import_hash);
CREATE INDEX import_rows_operation_idx ON import_rows (operation_id) WHERE operation_id IS NOT NULL;

CREATE TABLE import_account_map (
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source        TEXT    NOT NULL,
    source_name TEXT    NOT NULL,
    currency      TEXT    NOT NULL,
    account_id    INTEGER NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, source, source_name, currency)
) STRICT;

CREATE TABLE import_category_map (
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source        TEXT    NOT NULL,
    source_name TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    category_id   INTEGER NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, source, source_name, kind)
) STRICT;

CREATE INDEX import_rows_pair_idx ON import_rows (pair_row_id) WHERE pair_row_id IS NOT NULL;
CREATE INDEX import_account_map_account_idx ON import_account_map (account_id);
CREATE INDEX import_category_map_category_idx ON import_category_map (category_id);

CREATE TABLE rules (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stage      TEXT    NOT NULL,
    match      TEXT    NOT NULL,
    conditions TEXT    NOT NULL,
    actions    TEXT    NOT NULL,
    origin     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER
) STRICT;

CREATE INDEX rules_user_id_idx ON rules (user_id);

CREATE TABLE rule_settings (
    user_id     INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    is_learning INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER
) STRICT;

CREATE TABLE exchange_rates (
    source     TEXT    NOT NULL,
    currency   TEXT    NOT NULL,
    date       TEXT    NOT NULL,
    rate       TEXT    NOT NULL,
    is_manual  INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER,
    PRIMARY KEY (source, currency, date)
) STRICT;

CREATE VIEW account_balances AS
SELECT
    a.id AS account_id,
    CAST(COALESCE((
        SELECT SUM(CASE WHEN o.type IN ('expense', 'transfer') THEN -o.amount ELSE o.amount END)
        FROM operations AS o
        WHERE o.account_id = a.id
    ), 0) + COALESCE((
        SELECT SUM(o.amount) FROM operations AS o WHERE o.to_account_id = a.id
    ), 0) AS INTEGER) AS balance
FROM accounts AS a;

-- +goose Down
DROP VIEW account_balances;
DROP TABLE exchange_rates;
DROP TABLE rule_settings;
DROP TABLE rules;
DROP TABLE import_category_map;
DROP TABLE import_account_map;
DROP TABLE import_rows;
DROP TABLE operation_tags;
DROP TABLE tags;
DROP TABLE plan_carries;
DROP TABLE plans;
DROP TRIGGER operations_fts_update;
DROP TRIGGER operations_fts_delete;
DROP TRIGGER operations_fts_insert;
DROP TABLE operations_fts;
DROP TABLE operations;
DROP TABLE imports;
DROP TABLE categories;
DROP TABLE accounts;
DROP TABLE api_keys;
DROP TABLE sessions;
DROP TABLE user_settings;
DROP TABLE users;
