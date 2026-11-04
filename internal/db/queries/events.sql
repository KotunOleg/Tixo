-- name: CreateEvent :one
INSERT INTO events (organizer_id, venue_id, slug, title, description, starts_at, ends_at, genre, price_from, poster_key)
SELECT @organizer_id::bigint, v.id, @slug::text, @title::text, @description::text,
       @starts_at::timestamptz, @ends_at::timestamptz, @genre::text,
       sqlc.narg(price_from)::bigint, sqlc.narg(poster_key)::text
FROM venues v
WHERE v.id = @venue_id AND (v.organizer_id IS NULL OR v.organizer_id = @organizer_id::bigint)
RETURNING *;

-- name: GetEventForOrganizer :one
SELECT * FROM events WHERE id = @id AND organizer_id = @organizer_id;

-- name: ListEventsForOrganizer :many
SELECT * FROM events
WHERE organizer_id = @organizer_id
ORDER BY starts_at DESC, id DESC
LIMIT @lim OFFSET @off;

-- name: UpdateEvent :one
UPDATE events e
SET venue_id = v.id,
    slug = @slug,
    title = @title,
    description = @description,
    starts_at = @starts_at,
    ends_at = @ends_at,
    genre = @genre,
    price_from = sqlc.narg(price_from),
    poster_key = sqlc.narg(poster_key),
    updated_at = now()
FROM venues v
WHERE e.id = @id
  AND e.organizer_id = @organizer_id
  AND v.id = @venue_id
  AND (v.organizer_id IS NULL OR v.organizer_id = @organizer_id)
RETURNING e.*;

-- name: SetEventStatus :one
UPDATE events
SET status = @status, updated_at = now()
WHERE id = @id AND organizer_id = @organizer_id
RETURNING *;

-- name: DeleteEvent :execrows
DELETE FROM events WHERE id = @id AND organizer_id = @organizer_id;

-- name: GetPublishedEventBySlug :one
SELECT sqlc.embed(e), sqlc.embed(v)
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE e.slug = @slug AND e.status = 'published';

-- name: ListUpcomingPublishedEvents :many
SELECT sqlc.embed(e), v.name AS venue_name, v.city AS venue_city
FROM events e
JOIN venues v ON v.id = e.venue_id
WHERE e.status = 'published' AND e.ends_at >= @after
ORDER BY e.starts_at, e.id
LIMIT @lim OFFSET @off;

-- name: GetPublicEventBySlug :one
SELECT sqlc.embed(e), sqlc.embed(v), o.name AS organizer_name, o.contacts AS organizer_contacts
FROM events e
JOIN venues v ON v.id = e.venue_id
JOIN organizers o ON o.id = e.organizer_id
WHERE e.slug = @slug AND e.status IN ('published', 'cancelled');

-- name: ListMapEvents :many
SELECT e.slug, e.title, e.starts_at, e.genre,
       coalesce(p.price_from, 0)::bigint AS price_from, (p.price_from IS NOT NULL)::boolean AS has_price,
       v.id AS venue_id, v.name AS venue_name, v.city AS venue_city, v.address AS venue_address, v.lat AS venue_lat, v.lng AS venue_lng
FROM events e
JOIN venues v ON v.id = e.venue_id
LEFT JOIN LATERAL (
    SELECT coalesce(min(t.price) FILTER (WHERE t.available > 0), min(t.price))::bigint AS price_from
    FROM ticket_tiers t
    WHERE t.event_id = e.id
) p ON true
WHERE e.status = 'published'
  AND e.ends_at >= @after
  AND (sqlc.narg(before)::timestamptz IS NULL OR e.starts_at < sqlc.narg(before)::timestamptz)
  AND (sqlc.narg(genre)::text IS NULL OR e.genre = sqlc.narg(genre)::text)
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price_from <= sqlc.narg(max_price)::bigint)
ORDER BY v.city, v.name, v.id, e.starts_at, e.id
LIMIT @lim;

-- name: SearchPublicEvents :many
WITH needle AS (
    SELECT '%' || replace(replace(replace(sqlc.narg(query)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%' AS q,
           '%' || replace(replace(replace(sqlc.narg(city)::text, '\', '\\'), '%', '\%'), '_', '\_') || '%' AS city
)
SELECT e.slug, e.title, e.starts_at, e.genre, e.poster_key,
       coalesce(p.price_from, 0)::bigint AS price_from, (p.price_from IS NOT NULL)::boolean AS has_price,
       v.name AS venue_name, v.city AS venue_city
FROM events e
JOIN venues v ON v.id = e.venue_id
CROSS JOIN needle n
LEFT JOIN LATERAL (
    SELECT coalesce(min(t.price) FILTER (WHERE t.available > 0), min(t.price))::bigint AS price_from
    FROM ticket_tiers t
    WHERE t.event_id = e.id
) p ON true
WHERE e.status = 'published'
  AND e.ends_at >= @after
  AND (sqlc.narg(before)::timestamptz IS NULL OR e.starts_at < sqlc.narg(before)::timestamptz)
  AND (sqlc.narg(genre)::text IS NULL OR e.genre = sqlc.narg(genre)::text)
  AND (sqlc.narg(max_price)::bigint IS NULL OR p.price_from <= sqlc.narg(max_price)::bigint)
  AND (n.city IS NULL OR v.city ILIKE n.city)
  AND (n.q IS NULL
       OR e.title ILIKE n.q
       OR v.name ILIKE n.q
       OR v.city ILIKE n.q
       OR EXISTS (
           SELECT 1 FROM event_artists ea JOIN artists a ON a.id = ea.artist_id
           WHERE ea.event_id = e.id AND a.name ILIKE n.q
       ))
ORDER BY e.starts_at, e.id
LIMIT @lim;
