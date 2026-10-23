-- name: FeatureEnabledForOrganizer :one
SELECT (f.enabled_for_all
        OR o.plan = ANY (f.plans)
        OR EXISTS (SELECT 1 FROM feature_flag_organizers fo WHERE fo.name = f.name AND fo.organizer_id = o.id))::boolean AS enabled
FROM feature_flags f
CROSS JOIN organizers o
WHERE f.name = @name AND o.id = @organizer_id;

-- name: EnableFeatureForOrganizer :exec
INSERT INTO feature_flag_organizers (name, organizer_id)
VALUES (@name, @organizer_id)
ON CONFLICT DO NOTHING;

-- name: DisableFeatureForOrganizer :execrows
DELETE FROM feature_flag_organizers WHERE name = @name AND organizer_id = @organizer_id;

-- name: ListFeaturesForOrganizer :many
SELECT f.name,
       f.enabled_for_all,
       (o.plan = ANY (f.plans))::boolean AS by_plan,
       EXISTS (SELECT 1 FROM feature_flag_organizers fo WHERE fo.name = f.name AND fo.organizer_id = o.id)::boolean AS by_override
FROM feature_flags f
CROSS JOIN organizers o
WHERE o.id = @organizer_id
ORDER BY f.name;
