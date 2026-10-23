-- +goose Up
CREATE TABLE feature_flags (
    name            TEXT PRIMARY KEY CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    enabled_for_all BOOLEAN NOT NULL DEFAULT false,
    plans           TEXT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE feature_flag_organizers (
    name         TEXT NOT NULL REFERENCES feature_flags (name) ON DELETE CASCADE,
    organizer_id BIGINT NOT NULL REFERENCES organizers (id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (name, organizer_id)
);

CREATE INDEX feature_flag_organizers_organizer_id_idx ON feature_flag_organizers (organizer_id);

INSERT INTO feature_flags (name, enabled_for_all, plans) VALUES ('sales_analytics', false, '{premium}');

-- +goose Down
DROP TABLE feature_flag_organizers;
DROP TABLE feature_flags;
