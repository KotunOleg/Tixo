-- name: ReserveSeats :one
UPDATE ticket_tiers t
SET available = t.available - @quantity::integer
FROM events e
WHERE t.id = @tier_id
  AND e.id = t.event_id
  AND (sqlc.narg(event_id)::bigint IS NULL OR t.event_id = sqlc.narg(event_id)::bigint)
  AND e.status = 'published'
  AND e.starts_at > now()
  AND t.available >= @quantity::integer
RETURNING t.*;

-- name: GetTierSaleState :one
SELECT t.event_id, t.available, e.status, e.starts_at > now() AS upcoming
FROM ticket_tiers t
JOIN events e ON e.id = t.event_id
WHERE t.id = @tier_id;

-- name: CreateOrder :one
INSERT INTO orders (user_id, event_id, tier_id, quantity, unit_price, total, idempotency_key)
VALUES (@user_id, @event_id, @tier_id, @quantity, @unit_price, @total, @idempotency_key)
RETURNING *;

-- name: CreateTickets :many
INSERT INTO tickets (order_id)
SELECT @order_id::bigint FROM generate_series(1, @quantity::integer)
RETURNING *;

-- name: GetOrderByKey :one
SELECT * FROM orders WHERE user_id = @user_id AND idempotency_key = @idempotency_key;

-- name: GetOrderForUser :one
SELECT sqlc.embed(o), e.slug AS event_slug, e.title AS event_title, e.starts_at AS event_starts_at,
       e.status AS event_status, t.name AS tier_name, v.name AS venue_name, v.city AS venue_city
FROM orders o
JOIN events e ON e.id = o.event_id
JOIN ticket_tiers t ON t.id = o.tier_id
JOIN venues v ON v.id = e.venue_id
WHERE o.id = @id AND o.user_id = @user_id;

-- name: ListOrdersForUser :many
SELECT sqlc.embed(o), e.slug AS event_slug, e.title AS event_title, e.starts_at AS event_starts_at,
       e.status AS event_status, t.name AS tier_name, v.name AS venue_name, v.city AS venue_city
FROM orders o
JOIN events e ON e.id = o.event_id
JOIN ticket_tiers t ON t.id = o.tier_id
JOIN venues v ON v.id = e.venue_id
WHERE o.user_id = @user_id
ORDER BY o.created_at DESC, o.id DESC
LIMIT @lim OFFSET @off;

-- name: CountOrdersForUser :one
SELECT count(*)::integer FROM orders WHERE user_id = @user_id;

-- name: ListTicketsForOrder :many
SELECT * FROM tickets WHERE order_id = @order_id ORDER BY id;
