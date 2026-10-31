-- +goose Up
CREATE TABLE telegram_links (
    user_id          BIGINT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    chat_id          BIGINT,
    language         TEXT NOT NULL DEFAULT 'uk' CHECK (language IN ('uk', 'en')),
    token_hash       BYTEA,
    token_expires_at TIMESTAMPTZ,
    linked_at        TIMESTAMPTZ,
    CONSTRAINT telegram_links_chat_id_key UNIQUE (chat_id),
    CONSTRAINT telegram_links_token_hash_key UNIQUE (token_hash),
    CONSTRAINT telegram_links_token_has_expiry CHECK ((token_hash IS NULL) = (token_expires_at IS NULL)),
    CONSTRAINT telegram_links_chat_has_linked_at CHECK ((chat_id IS NULL) = (linked_at IS NULL))
);

CREATE TABLE notification_log (
    user_id        BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event_id       BIGINT NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind <> ''),
    queued_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at        TIMESTAMPTZ,
    skipped_reason TEXT,
    PRIMARY KEY (user_id, event_id, kind)
);

CREATE INDEX notification_log_event_id_idx ON notification_log (event_id);

-- +goose Down
DROP TABLE notification_log;
DROP TABLE telegram_links;
