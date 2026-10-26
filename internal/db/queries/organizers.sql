-- name: CreateOrganizer :one
INSERT INTO organizers (slug, name, contacts)
VALUES (@slug, @name, @contacts)
RETURNING *;

-- name: GetOrganizer :one
SELECT * FROM organizers WHERE id = $1;

-- name: GetOrganizerBySlug :one
SELECT * FROM organizers WHERE slug = $1;

-- name: UpdateOrganizer :one
UPDATE organizers
SET name = @name, contacts = @contacts
WHERE id = @id
RETURNING *;

-- name: SetOrganizerPlan :one
UPDATE organizers SET plan = @plan WHERE id = @id RETURNING *;

-- name: AddMembership :one
INSERT INTO memberships (user_id, organizer_id, role)
VALUES (@user_id, @organizer_id, @role)
RETURNING *;

-- name: GetMembership :one
SELECT * FROM memberships WHERE user_id = @user_id AND organizer_id = @organizer_id;

-- name: RemoveMembership :execrows
DELETE FROM memberships WHERE user_id = @user_id AND organizer_id = @organizer_id;

-- name: ListOrganizersForUser :many
SELECT sqlc.embed(o), m.role
FROM organizers o
JOIN memberships m ON m.organizer_id = o.id
WHERE m.user_id = @user_id
ORDER BY o.name, o.id;

-- name: GetOrganizerForMember :one
SELECT sqlc.embed(o), m.role
FROM organizers o
JOIN memberships m ON m.organizer_id = o.id
WHERE o.slug = @slug AND m.user_id = @user_id;

-- name: ListMembers :many
SELECT u.id, u.name, u.email, m.role
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.organizer_id = @organizer_id
ORDER BY m.role = 'owner' DESC, u.name, u.id;
