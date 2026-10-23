CREATE TABLE orders (
    id              TEXT PRIMARY KEY,
    source          TEXT NOT NULL CHECK (source IN ('itemku', 'direct')),
    itemku_order_id INTEGER UNIQUE,
    order_number    TEXT NOT NULL DEFAULT '',
    product_id      INTEGER,
    sku_override    TEXT,
    game_name       TEXT NOT NULL DEFAULT '',
    product_name    TEXT NOT NULL DEFAULT '',
    price           INTEGER NOT NULL DEFAULT 0,
    quantity        INTEGER NOT NULL DEFAULT 1,
    order_income    INTEGER NOT NULL DEFAULT 0,
    required_info   TEXT NOT NULL DEFAULT '{}',
    using_delivery_info INTEGER NOT NULL DEFAULT 0,
    customer_no     TEXT NOT NULL DEFAULT '',
    buyer_name      TEXT NOT NULL DEFAULT '',
    buyer_wa        TEXT NOT NULL DEFAULT '',
    paid_price      INTEGER NOT NULL DEFAULT 0,
    state           TEXT NOT NULL DEFAULT 'new',
    reason          TEXT NOT NULL DEFAULT '',
    detail          TEXT NOT NULL DEFAULT '',
    deadline_at     INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    delivered_at    INTEGER
);

CREATE INDEX orders_state_deadline ON orders(state, deadline_at);

CREATE TABLE attempts (
    ref_id          TEXT PRIMARY KEY,
    order_id        TEXT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    unit            INTEGER NOT NULL,
    attempt_no      INTEGER NOT NULL,
    buyer_sku_code  TEXT NOT NULL,
    customer_no     TEXT NOT NULL,
    max_price       INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'unknown'
                    CHECK (status IN ('unknown', 'Pending', 'Sukses', 'Gagal')),
    rc              TEXT NOT NULL DEFAULT '',
    message         TEXT NOT NULL DEFAULT '',
    sn              TEXT NOT NULL DEFAULT '',
    price           INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

-- The double-buy guard: at most one attempt per unit may be open at a time.
CREATE UNIQUE INDEX attempts_one_open ON attempts(order_id, unit) WHERE status <> 'Gagal';

CREATE TABLE products (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL DEFAULT '',
    game_name       TEXT NOT NULL DEFAULT '',
    item_type       TEXT NOT NULL DEFAULT '',
    itemku_price    INTEGER NOT NULL DEFAULT 0,
    itemku_stock    INTEGER NOT NULL DEFAULT 0,
    mode            TEXT NOT NULL DEFAULT 'manual' CHECK (mode IN ('off', 'manual', 'auto')),
    off_reason      TEXT NOT NULL DEFAULT '',
    saved_stock     INTEGER,
    saved_mode      TEXT,
    deadline_class  TEXT NOT NULL DEFAULT 'regular'
                    CHECK (deadline_class IN ('regular', 'ten_min', 'instant')),
    stock_locked    INTEGER NOT NULL DEFAULT 0,
    fee_rate        REAL,
    last_seller     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE product_skus (
    product_id      INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    buyer_sku_code  TEXT NOT NULL,
    PRIMARY KEY (product_id, position)
);

CREATE TABLE pricelist (
    buyer_sku_code       TEXT PRIMARY KEY,
    product_name         TEXT NOT NULL DEFAULT '',
    category             TEXT NOT NULL DEFAULT '',
    brand                TEXT NOT NULL DEFAULT '',
    type                 TEXT NOT NULL DEFAULT '',
    seller_name          TEXT NOT NULL DEFAULT '',
    price                INTEGER NOT NULL DEFAULT 0,
    buyer_product_status INTEGER NOT NULL DEFAULT 0,
    seller_product_status INTEGER NOT NULL DEFAULT 0,
    unlimited_stock      INTEGER NOT NULL DEFAULT 0,
    stock                INTEGER NOT NULL DEFAULT 0,
    multi                INTEGER NOT NULL DEFAULT 0,
    start_cut_off        TEXT NOT NULL DEFAULT '',
    end_cut_off          TEXT NOT NULL DEFAULT '',
    synced_at            INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE templates (
    seller_name     TEXT NOT NULL,
    game_name       TEXT NOT NULL,
    template        TEXT NOT NULL,
    PRIMARY KEY (seller_name, game_name)
);

CREATE TABLE settings (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL
);

CREATE TABLE api_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    at              INTEGER NOT NULL,
    service         TEXT NOT NULL DEFAULT '',
    endpoint        TEXT NOT NULL DEFAULT '',
    req             TEXT NOT NULL DEFAULT '',
    resp            TEXT NOT NULL DEFAULT '',
    http_status     INTEGER NOT NULL DEFAULT 0,
    ms              INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX api_log_at ON api_log(at);

CREATE TABLE alerts_sent (
    key             TEXT PRIMARY KEY,
    at              INTEGER NOT NULL
);
