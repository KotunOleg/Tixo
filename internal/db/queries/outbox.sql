-- name: InsertOutbox :one
INSERT INTO outbox (topic, payload) VALUES (@topic, @payload) RETURNING *;

-- name: ClaimOutboxBatch :many
SELECT * FROM outbox
WHERE published_at IS NULL
ORDER BY id
LIMIT @lim
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxPublished :exec
UPDATE outbox SET published_at = now() WHERE id = @id;
