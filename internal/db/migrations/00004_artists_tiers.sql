-- +goose Up
CREATE TABLE artists (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    mbid           UUID,
    name           TEXT NOT NULL CHECK (name <> ''),
    sort_name      TEXT NOT NULL DEFAULT '',
    type           TEXT,
    country        TEXT CHECK (country ~ '^[A-Z]{2}$'),
    disambiguation TEXT NOT NULL DEFAULT '',
    genres         TEXT[] NOT NULL DEFAULT '{}',
    links          JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(links) = 'object'),
    enriched_at    TIMESTAMPTZ,
    CONSTRAINT artists_mbid_key UNIQUE (mbid)
);

CREATE INDEX artists_name_trgm_idx ON artists USING GIN (name gin_trgm_ops);

CREATE TABLE event_artists (
    event_id     BIGINT NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    artist_id    BIGINT NOT NULL REFERENCES artists (id) ON DELETE RESTRICT,
    position     INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    is_headliner BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (event_id, artist_id)
);

CREATE INDEX event_artists_artist_id_idx ON event_artists (artist_id);

CREATE TABLE ticket_tiers (
    id        BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id  BIGINT NOT NULL REFERENCES events (id) ON DELETE RESTRICT,
    name      TEXT NOT NULL CHECK (name <> ''),
    price     BIGINT NOT NULL CHECK (price >= 0),
    quantity  INTEGER NOT NULL CHECK (quantity > 0),
    available INTEGER NOT NULL,
    CONSTRAINT ticket_tiers_event_name_key UNIQUE (event_id, name),
    CONSTRAINT ticket_tiers_available_range CHECK (available >= 0 AND available <= quantity)
);

-- +goose Down
DROP TABLE ticket_tiers;
DROP TABLE event_artists;
DROP TABLE artists;
