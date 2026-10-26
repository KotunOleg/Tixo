-- name: CreateVenue :one
INSERT INTO venues (organizer_id, name, address, city, lat, lng, capacity)
VALUES (@organizer_id::bigint, @name, @address, @city, @lat, @lng, @capacity)
RETURNING *;

-- name: GetVenue :one
SELECT * FROM venues WHERE id = $1;

-- name: ListVenuesForOrganizer :many
SELECT * FROM venues
WHERE organizer_id = @organizer_id::bigint OR organizer_id IS NULL
ORDER BY organizer_id NULLS LAST, name, id;

-- name: UpdateVenue :one
UPDATE venues
SET name = @name, address = @address, city = @city, lat = @lat, lng = @lng, capacity = @capacity
WHERE id = @id AND organizer_id = @organizer_id::bigint
RETURNING *;

-- name: DeleteVenue :execrows
DELETE FROM venues WHERE id = @id AND organizer_id = @organizer_id::bigint;
