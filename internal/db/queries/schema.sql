-- name: SchemaStatus :one
SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_extension WHERE extname = 'pg_trgm') AS trgm_installed;
