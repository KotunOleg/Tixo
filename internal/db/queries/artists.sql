-- name: SearchArtists :many
SELECT * FROM artists
WHERE name ILIKE '%' || replace(replace(replace(@query::text, '\', '\\'), '%', '\%'), '_', '\_') || '%'
ORDER BY name <-> @query::text, name, id
LIMIT @lim;

-- name: GetArtist :one
SELECT * FROM artists WHERE id = $1;

-- name: GetArtistByMBID :one
SELECT * FROM artists WHERE mbid = $1;

-- name: CreateArtist :one
INSERT INTO artists (name, sort_name, type, disambiguation)
VALUES (@name, @sort_name, sqlc.narg(type), @disambiguation)
RETURNING *;
