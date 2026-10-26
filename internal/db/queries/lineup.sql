-- name: SetEventArtist :execrows
INSERT INTO event_artists (event_id, artist_id, position, is_headliner)
SELECT e.id, @artist_id::bigint, @position::integer, @is_headliner::boolean
FROM events e
WHERE e.id = @event_id AND e.organizer_id = @organizer_id
ON CONFLICT (event_id, artist_id) DO UPDATE
SET position = EXCLUDED.position, is_headliner = EXCLUDED.is_headliner;

-- name: RemoveEventArtist :execrows
DELETE FROM event_artists ea
USING events e
WHERE ea.event_id = e.id
  AND e.id = @event_id
  AND e.organizer_id = @organizer_id
  AND ea.artist_id = @artist_id;

-- name: ListEventArtists :many
SELECT sqlc.embed(a), ea.position, ea.is_headliner
FROM event_artists ea
JOIN artists a ON a.id = ea.artist_id
WHERE ea.event_id = $1
ORDER BY ea.is_headliner DESC, ea.position, a.name;
