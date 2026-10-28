-- name: SalesByDayForOrganizer :many
SELECT (o.created_at AT TIME ZONE 'Europe/Kyiv')::date AS day,
       sum(o.quantity)::bigint AS tickets,
       sum(o.total)::bigint AS revenue
FROM orders o
JOIN events e ON e.id = o.event_id
WHERE e.organizer_id = @organizer_id
  AND o.status IN ('paid', 'fulfilled')
  AND o.created_at >= @since::timestamptz
  AND o.created_at < @until::timestamptz
GROUP BY 1
ORDER BY 1;

-- name: SalesByEventForOrganizer :many
SELECT e.id, e.slug, e.title,
       sum(o.quantity)::bigint AS tickets,
       sum(o.total)::bigint AS revenue
FROM orders o
JOIN events e ON e.id = o.event_id
WHERE e.organizer_id = @organizer_id
  AND o.status IN ('paid', 'fulfilled')
  AND o.created_at >= @since::timestamptz
  AND o.created_at < @until::timestamptz
GROUP BY e.id
ORDER BY tickets DESC, revenue DESC, e.title, e.id;

-- name: EventsByGenreForOrganizer :many
SELECT genre, count(*)::bigint AS events
FROM events
WHERE organizer_id = @organizer_id AND status <> 'cancelled'
GROUP BY genre
ORDER BY events DESC, genre;

-- name: CountPublishedEventsForOrganizer :one
SELECT count(*)::bigint FROM events WHERE organizer_id = @organizer_id AND status = 'published';
