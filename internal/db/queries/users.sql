-- name: CreateUser :one
INSERT INTO users (email, name, google_sub)
VALUES (lower(@email), @name, sqlc.narg(google_sub))
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = lower(@email);

-- name: UpdateGoogleUser :one
UPDATE users
SET email = lower(@email), name = @name
WHERE google_sub = @google_sub
RETURNING *;

-- name: LinkGoogleAccount :one
UPDATE users
SET google_sub = @google_sub, name = @name
WHERE email = lower(@email) AND google_sub IS NULL
RETURNING *;
