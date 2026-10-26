-- name: CreateTicketTier :one
INSERT INTO ticket_tiers (event_id, name, price, quantity, available)
SELECT e.id, sqlc.arg(name)::text, sqlc.arg(price)::bigint, sqlc.arg(quantity)::integer, sqlc.arg(quantity)::integer
FROM events e
WHERE e.id = sqlc.arg(event_id) AND e.organizer_id = sqlc.arg(organizer_id)
RETURNING *;

-- name: UpdateTicketTier :one
UPDATE ticket_tiers t
SET name = sqlc.arg(name)::text,
    price = sqlc.arg(price)::bigint,
    quantity = sqlc.arg(quantity)::integer,
    available = t.available + (sqlc.arg(quantity)::integer - t.quantity)
FROM events e
WHERE t.id = sqlc.arg(id)
  AND t.event_id = e.id
  AND e.organizer_id = sqlc.arg(organizer_id)
RETURNING t.*;

-- name: DeleteUnsoldTicketTier :execrows
DELETE FROM ticket_tiers t
USING events e
WHERE t.id = @id
  AND t.event_id = e.id
  AND e.organizer_id = @organizer_id
  AND t.available = t.quantity;

-- name: ListTicketTiers :many
SELECT * FROM ticket_tiers WHERE event_id = $1 ORDER BY price, id;

-- name: ListTicketTiersForOrganizer :many
SELECT t.* FROM ticket_tiers t
JOIN events e ON e.id = t.event_id
WHERE t.event_id = sqlc.arg(event_id) AND e.organizer_id = sqlc.arg(organizer_id)
ORDER BY t.price, t.id;

-- name: GetTicketTier :one
SELECT t.* FROM ticket_tiers t
JOIN events e ON e.id = t.event_id
WHERE t.id = sqlc.arg(id) AND e.organizer_id = sqlc.arg(organizer_id);
