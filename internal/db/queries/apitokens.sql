-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash)
VALUES (@user_id, @name, @token_hash)
RETURNING *;

-- name: ListAPITokensForUser :many
SELECT * FROM api_tokens WHERE user_id = @user_id ORDER BY created_at DESC;

-- name: GetUserByAPITokenHash :one
SELECT u.* FROM users u
JOIN api_tokens t ON t.user_id = u.id
WHERE t.token_hash = @token_hash;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now() WHERE token_hash = @token_hash;

-- name: DeleteAPIToken :execrows
DELETE FROM api_tokens WHERE id = @id AND user_id = @user_id;
