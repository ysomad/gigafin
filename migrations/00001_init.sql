-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    login         TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    timezone      TEXT    NOT NULL,
    -- user or admin, see domain.Role
    role          TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    -- NULL until the user is first changed
    updated_at    INTEGER
) STRICT;

-- display preferences; a user without a row has the defaults
CREATE TABLE user_settings (
    user_id          INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    show_minor_units INTEGER NOT NULL,
    -- ISO code totals are converted to, empty when rates are off
    primary_currency TEXT    NOT NULL,
    -- exchange rate source, empty when rates are off, see domain.RateSource
    rate_source      TEXT    NOT NULL,
    -- UI language code, empty to follow the browser, see i18n.Languages
    language         TEXT    NOT NULL,
    created_at       INTEGER NOT NULL,
    -- NULL until the settings are first changed
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

-- one key opens both /api and /mcp, limited by scope
CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    prefix       TEXT    NOT NULL,
    key_hash     TEXT    NOT NULL,
    scope        TEXT    NOT NULL,
    -- NULL for a key that never expires
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
    -- kind tells assets from liabilities. Balances stay signed: a credit card owing 500
    -- has balance -500, the UI shows it as owed.
    kind        TEXT    NOT NULL,
    -- minor units of the account currency, credit cards only
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

-- parent_id is NULL for cat1, so a plain UNIQUE (user_id, parent_id, name) would not
-- stop duplicate cat1 names; one partial index per level covers both.
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
    -- category of an expense or income, NULL for transfers and adjustments
    category_id     INTEGER REFERENCES categories (id) ON DELETE RESTRICT,
    -- id of the operation a split started from, shared by all its parts; NULL when never
    -- split. No foreign key: the parts stay a group after the original is deleted.
    origin_id       INTEGER,
    -- minor units of the account currency
    amount          INTEGER NOT NULL,
    date            TEXT    NOT NULL,
    time            TEXT,
    payee           TEXT,
    -- payee as the bank sent it, set on import and never changed; rules match it after
    -- the payee is renamed. NULL for operations entered by hand.
    source_payee    TEXT,
    note            TEXT,
    -- Merchant Category Code of a card payment, NULL when the source has none
    mcc             TEXT,
    import_id       INTEGER REFERENCES imports (id) ON DELETE SET NULL,
    import_hash     TEXT,
    created_at      INTEGER NOT NULL,
    -- NULL until the operation is first changed
    updated_at      INTEGER,
    UNIQUE (user_id, import_hash)
) STRICT;

CREATE INDEX operations_user_date_idx ON operations (user_id, date DESC, id DESC);
CREATE INDEX operations_account_date_idx ON operations (account_id, date);
CREATE INDEX operations_to_account_date_idx ON operations (to_account_id, date) WHERE to_account_id IS NOT NULL;
CREATE INDEX operations_import_idx ON operations (import_id) WHERE import_id IS NOT NULL;
CREATE INDEX operations_category_idx ON operations (category_id) WHERE category_id IS NOT NULL;

-- full-text index of payee and note for search, case-insensitive for any script.
-- Contentless, kept in sync with operations by the triggers below; body is payee and
-- note joined, so all words of a query may match across both. The tokenizer does not
-- fold ё, so it is indexed as е and the search does the same.
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
    -- 1 to copy into following months, see domain.CarryPlans
    is_repeating INTEGER NOT NULL,
    UNIQUE (category_id, month, currency)
) STRICT;

CREATE INDEX plans_user_month_idx ON plans (user_id, month);

-- months repeating plans were already carried into, so each is filled once
CREATE TABLE plan_carries (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    month      TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, month)
) STRICT;

-- labels across categories, such as a trip; created with the first operation that uses
-- one and deleted with the last
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
    -- one bank account name can hold several currencies
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

-- SQLite looks up child rows on every parent delete; without these indexes deleting
-- import rows or categories scans whole tables per row.
CREATE INDEX import_rows_pair_idx ON import_rows (pair_row_id) WHERE pair_row_id IS NOT NULL;
CREATE INDEX import_account_map_account_idx ON import_account_map (account_id);
CREATE INDEX import_category_map_category_idx ON import_category_map (category_id);

-- categorization rules, see domain.Rule; conditions and actions are JSON arrays
CREATE TABLE rules (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- see domain.RuleStage
    stage      TEXT    NOT NULL,
    -- see domain.RuleMatch
    match      TEXT    NOT NULL,
    conditions TEXT    NOT NULL,
    actions    TEXT    NOT NULL,
    -- who made the rule, see domain.RuleOrigin
    origin     TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    -- NULL until the rule is first changed
    updated_at INTEGER
) STRICT;

CREATE INDEX rules_user_id_idx ON rules (user_id);

-- a user without a row has the defaults, see domain.RuleSettings
CREATE TABLE rule_settings (
    user_id     INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- 1 when changing the category of a payee's operations creates a rule
    is_learning INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    -- NULL until the settings are first changed
    updated_at  INTEGER
) STRICT;

-- daily exchange rates shared by all users: one row per day of each currency, days
-- without a published rate carry the last one, see domain.FillRates
CREATE TABLE exchange_rates (
    -- see domain.RateSource
    source     TEXT    NOT NULL,
    currency   TEXT    NOT NULL,
    date       TEXT    NOT NULL,
    -- decimal units of the source pivot currency per one unit of currency
    rate       TEXT    NOT NULL,
    -- 1 for rates entered or imported by a user, never overwritten by a sync
    is_manual  INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    -- NULL until the rate is first changed
    updated_at INTEGER,
    PRIMARY KEY (source, currency, date)
) STRICT;

-- balance in minor units: expenses and transfers out subtract, income and adjustments
-- add with their sign, transfers in add
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
