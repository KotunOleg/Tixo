-- +goose Up
CREATE TABLE orders (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    event_id        BIGINT NOT NULL REFERENCES events (id) ON DELETE RESTRICT,
    tier_id         BIGINT NOT NULL REFERENCES ticket_tiers (id) ON DELETE RESTRICT,
    quantity        INTEGER NOT NULL CHECK (quantity BETWEEN 1 AND 10),
    unit_price      BIGINT NOT NULL CHECK (unit_price >= 0),
    total           BIGINT NOT NULL CHECK (total >= 0),
    status          TEXT NOT NULL DEFAULT 'paid' CHECK (status IN ('pending', 'paid', 'fulfilled', 'cancelled')),
    idempotency_key UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT orders_user_idempotency_key UNIQUE (user_id, idempotency_key)
);

CREATE INDEX orders_user_id_created_at_idx ON orders (user_id, created_at DESC);
CREATE INDEX orders_event_id_idx ON orders (event_id);
CREATE INDEX orders_tier_id_idx ON orders (tier_id);

CREATE TABLE tickets (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id  BIGINT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    code      UUID NOT NULL DEFAULT gen_random_uuid(),
    pdf_key   TEXT,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tickets_code_key UNIQUE (code)
);

CREATE INDEX tickets_order_id_idx ON tickets (order_id);

-- +goose Down
DROP TABLE tickets;
DROP TABLE orders;
