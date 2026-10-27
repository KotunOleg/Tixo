-- name: SeedUser :one
INSERT INTO users (email, name)
VALUES (lower(@email), @name)
ON CONFLICT (email) DO UPDATE SET email = users.email
RETURNING id;

-- name: SeedOrganizer :one
INSERT INTO organizers (slug, name, contacts, plan)
VALUES (@slug, @name, @contacts, @plan)
ON CONFLICT (slug) DO UPDATE SET slug = organizers.slug
RETURNING id;

-- name: SeedMembership :exec
INSERT INTO memberships (user_id, organizer_id, role)
VALUES (@user_id, @organizer_id, @role)
ON CONFLICT (user_id, organizer_id) DO NOTHING;

-- name: SeedVenue :one
INSERT INTO venues (organizer_id, name, address, city, lat, lng, capacity)
VALUES (sqlc.narg(organizer_id), @name, @address, @city, @lat, @lng, @capacity)
ON CONFLICT ON CONSTRAINT venues_organizer_name_key DO UPDATE
SET address = EXCLUDED.address, city = EXCLUDED.city, lat = EXCLUDED.lat, lng = EXCLUDED.lng, capacity = EXCLUDED.capacity
RETURNING id;

-- name: SeedEvent :one
INSERT INTO events (organizer_id, venue_id, slug, title, description, starts_at, ends_at, genre, price_from, status)
VALUES (@organizer_id, @venue_id, @slug, @title, @description, @starts_at, @ends_at, @genre, @price_from, @status)
ON CONFLICT (slug) DO UPDATE
SET venue_id = EXCLUDED.venue_id,
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    genre = EXCLUDED.genre,
    price_from = EXCLUDED.price_from,
    status = EXCLUDED.status,
    updated_at = now()
WHERE events.organizer_id = EXCLUDED.organizer_id
RETURNING id;

-- name: SeedArtist :one
INSERT INTO artists (mbid, name, sort_name, type, country)
VALUES (@mbid, @name, @sort_name, @type, sqlc.narg(country))
ON CONFLICT (mbid) DO UPDATE SET mbid = artists.mbid
RETURNING id;

-- name: SeedTicketTier :exec
INSERT INTO ticket_tiers (event_id, name, price, quantity, available)
VALUES (@event_id, @name, @price, @quantity, @available)
ON CONFLICT (event_id, name) DO NOTHING;

-- name: SeedOwner :exec
INSERT INTO memberships (user_id, organizer_id, role)
VALUES (@user_id, @organizer_id, 'owner')
ON CONFLICT (user_id, organizer_id) DO UPDATE SET role = 'owner';
