-- name: IssueTelegramLinkToken :exec
INSERT INTO telegram_links (user_id, token_hash, token_expires_at)
VALUES (@user_id, @token_hash, @expires_at::timestamptz)
ON CONFLICT (user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, token_expires_at = EXCLUDED.token_expires_at;

-- name: ConsumeTelegramLinkToken :one
UPDATE telegram_links
SET token_hash = NULL, token_expires_at = NULL
WHERE token_hash = @token_hash AND token_expires_at > @now::timestamptz
RETURNING user_id;

-- name: ReleaseTelegramChat :exec
UPDATE telegram_links
SET chat_id = NULL, linked_at = NULL
WHERE chat_id = @chat_id::bigint;

-- name: LinkTelegramChat :exec
UPDATE telegram_links
SET chat_id = @chat_id::bigint, language = @language, linked_at = @linked_at::timestamptz
WHERE user_id = @user_id;

-- name: UnlinkTelegramUser :exec
UPDATE telegram_links
SET chat_id = NULL, linked_at = NULL, token_hash = NULL, token_expires_at = NULL
WHERE user_id = @user_id;

-- name: UnlinkTelegramChat :execrows
UPDATE telegram_links
SET chat_id = NULL, linked_at = NULL
WHERE chat_id = @chat_id::bigint;

-- name: GetTelegramLink :one
SELECT * FROM telegram_links WHERE user_id = @user_id;

-- name: QueueEventReminders :many
INSERT INTO notification_log (user_id, event_id, kind)
SELECT DISTINCT o.user_id, e.id, @kind::text
FROM events e
JOIN orders o ON o.event_id = e.id AND o.status IN ('paid', 'fulfilled')
JOIN telegram_links l ON l.user_id = o.user_id AND l.chat_id IS NOT NULL
WHERE e.status = 'published' AND e.starts_at > @now AND e.starts_at <= @until
ON CONFLICT (user_id, event_id, kind) DO NOTHING
RETURNING user_id, event_id, kind;

-- name: GetEventReminder :one
SELECT n.sent_at, n.skipped_reason,
       e.title, e.slug, e.starts_at, e.status,
       v.name AS venue_name, v.city AS venue_city,
       l.chat_id, COALESCE(l.language, 'uk')::text AS language
FROM notification_log n
JOIN events e ON e.id = n.event_id
JOIN venues v ON v.id = e.venue_id
LEFT JOIN telegram_links l ON l.user_id = n.user_id
WHERE n.user_id = @user_id AND n.event_id = @event_id AND n.kind = @kind;

-- name: MarkNotificationSent :exec
UPDATE notification_log SET sent_at = @sent_at::timestamptz
WHERE user_id = @user_id AND event_id = @event_id AND kind = @kind;

-- name: MarkNotificationSkipped :exec
UPDATE notification_log SET skipped_reason = @reason::text
WHERE user_id = @user_id AND event_id = @event_id AND kind = @kind AND sent_at IS NULL;
