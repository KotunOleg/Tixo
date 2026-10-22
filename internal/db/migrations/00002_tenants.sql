-- +goose Up
CREATE TABLE users (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      TEXT NOT NULL CHECK (email <> '' AND email = lower(email)),
    name       TEXT NOT NULL,
    google_sub TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT users_email_key UNIQUE (email)
);

CREATE TABLE organizers (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug       TEXT NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name       TEXT NOT NULL CHECK (name <> ''),
    contacts   JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(contacts) = 'object'),
    plan       TEXT NOT NULL DEFAULT 'free' CHECK (plan IN ('free', 'premium')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT organizers_slug_key UNIQUE (slug)
);

CREATE TABLE memberships (
    user_id      BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    organizer_id BIGINT NOT NULL REFERENCES organizers (id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('owner', 'editor')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organizer_id)
);

CREATE INDEX memberships_organizer_id_idx ON memberships (organizer_id);

-- +goose Down
DROP TABLE memberships;
DROP TABLE organizers;
DROP TABLE users;
