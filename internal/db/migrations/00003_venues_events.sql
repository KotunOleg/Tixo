-- +goose Up
CREATE TABLE venues (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organizer_id BIGINT REFERENCES organizers (id),
    name         TEXT NOT NULL CHECK (name <> ''),
    address      TEXT NOT NULL,
    city         TEXT NOT NULL CHECK (city <> ''),
    lat          DOUBLE PRECISION NOT NULL CHECK (lat BETWEEN -90 AND 90),
    lng          DOUBLE PRECISION NOT NULL CHECK (lng BETWEEN -180 AND 180),
    capacity     INTEGER NOT NULL CHECK (capacity > 0),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT venues_organizer_name_key UNIQUE NULLS NOT DISTINCT (organizer_id, name)
);

CREATE INDEX venues_city_idx ON venues (city);

CREATE TABLE events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organizer_id BIGINT NOT NULL REFERENCES organizers (id),
    venue_id     BIGINT NOT NULL REFERENCES venues (id) ON DELETE RESTRICT,
    slug         TEXT NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title        TEXT NOT NULL CHECK (title <> ''),
    description  TEXT NOT NULL DEFAULT '',
    starts_at    TIMESTAMPTZ NOT NULL,
    ends_at      TIMESTAMPTZ NOT NULL,
    genre        TEXT NOT NULL,
    price_from   BIGINT CHECK (price_from >= 0),
    poster_key   TEXT,
    status       TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT events_slug_key UNIQUE (slug),
    CONSTRAINT events_ends_after_start CHECK (ends_at > starts_at)
);

CREATE INDEX events_status_starts_at_idx ON events (status, starts_at);
CREATE INDEX events_organizer_id_idx ON events (organizer_id);
CREATE INDEX events_venue_id_idx ON events (venue_id);
CREATE INDEX events_genre_idx ON events (genre);

-- +goose Down
DROP TABLE events;
DROP TABLE venues;
