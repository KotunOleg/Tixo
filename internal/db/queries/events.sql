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
