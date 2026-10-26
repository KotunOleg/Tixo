-- name: CreateUser :one
INSERT INTO users (email, name, google_sub)
VALUES (lower(@email), @name, sqlc.narg(google_sub))
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = lower(@email);
