package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
	StatusCancelled = "cancelled"

	FieldVenue     = "venue_id"
	FieldSlug      = "slug"
	FieldTitle     = "title"
	FieldStartsAt  = "starts_at"
	FieldEndsAt    = "ends_at"
	FieldGenre     = "genre"
	FieldPriceFrom = "price_from"

	CodeRequired = "required"
	CodeFormat   = "format"
	CodeLength   = "length"
	CodeRange    = "range"
	CodeTaken    = "taken"
	CodeInvalid  = "invalid"
	CodeNotFound = "not_found"

	slugMinLen  = 3
	slugMaxLen  = 80
	titleMaxLen = 200

	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
	checkViolation      = "23514"
	slugConstraint      = "events_slug_key"
	endsAfterStart      = "events_ends_after_start"
)

var (
	ErrNotFound   = errors.New("event not found")
	ErrHasTickets = errors.New("event has ticket tiers and cannot be deleted")
	ErrBadStatus  = errors.New("invalid event status")

	slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for f, code := range e.Fields {
		parts = append(parts, f+": "+code)
	}
	slices.Sort(parts)
	return "invalid event: " + strings.Join(parts, ", ")
}

type Input struct {
	VenueID     int64
	Slug        string
	Title       string
	Description string
	StartsAt    time.Time
	EndsAt      time.Time
	Genre       string
	PriceFrom   *int64
	PosterKey   string
}

type Public struct {
	Event         dbgen.Event
	Venue         dbgen.Venue
	OrganizerName string
	Contacts      map[string]string
	Lineup        []dbgen.ListEventArtistsRow
	Tiers         []dbgen.TicketTier
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) List(ctx context.Context, organizerID int64, limit, offset int32) ([]dbgen.Event, error) {
	return s.q.ListEventsForOrganizer(ctx, dbgen.ListEventsForOrganizerParams{OrganizerID: organizerID, Lim: limit, Off: offset})
}

func (s *Service) Get(ctx context.Context, organizerID, id int64) (dbgen.Event, error) {
	e, err := s.q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: id, OrganizerID: organizerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Event{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Event{}, fmt.Errorf("get event: %w", err)
	}
	return e, nil
}

func (s *Service) PublicBySlug(ctx context.Context, slug string) (Public, error) {
	row, err := s.q.GetPublicEventBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Public{}, ErrNotFound
	}
	if err != nil {
		return Public{}, fmt.Errorf("get public event: %w", err)
	}
	lineup, err := s.q.ListEventArtists(ctx, row.Event.ID)
	if err != nil {
		return Public{}, fmt.Errorf("list lineup: %w", err)
	}
	tiers, err := s.q.ListTicketTiers(ctx, row.Event.ID)
	if err != nil {
		return Public{}, fmt.Errorf("list tiers: %w", err)
	}
	var contacts map[string]string
	if err := json.Unmarshal(row.OrganizerContacts, &contacts); err != nil {
		return Public{}, fmt.Errorf("decode organizer contacts: %w", err)
	}
	return Public{
		Event: row.Event, Venue: row.Venue, OrganizerName: row.OrganizerName, Contacts: contacts, Lineup: lineup, Tiers: tiers,
	}, nil
}

func (s *Service) Upcoming(ctx context.Context, now time.Time, limit int32) ([]dbgen.ListUpcomingPublishedEventsRow, error) {
	return s.q.ListUpcomingPublishedEvents(ctx, dbgen.ListUpcomingPublishedEventsParams{After: now, Lim: limit})
}

func (s *Service) Create(ctx context.Context, organizerID int64, in Input) (dbgen.Event, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.Event{}, err
	}
	e, err := s.q.CreateEvent(ctx, dbgen.CreateEventParams{
		OrganizerID: organizerID, VenueID: in.VenueID, Slug: in.Slug, Title: in.Title, Description: in.Description,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, Genre: in.Genre, PriceFrom: priceParam(in.PriceFrom), PosterKey: posterParam(in.PosterKey),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Event{}, badVenue()
	}
	if dup := slugTaken(err); dup != nil {
		return dbgen.Event{}, dup
	}
	if rangeErr := endsBeforeStarts(err); rangeErr != nil {
		return dbgen.Event{}, rangeErr
	}
	if err != nil {
		return dbgen.Event{}, fmt.Errorf("create event: %w", err)
	}
	return e, nil
}

func (s *Service) Update(ctx context.Context, organizerID, id int64, in Input) (dbgen.Event, error) {
	if _, err := s.Get(ctx, organizerID, id); err != nil {
		return dbgen.Event{}, err
	}
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.Event{}, err
	}
	e, err := s.q.UpdateEvent(ctx, dbgen.UpdateEventParams{
		Slug: in.Slug, Title: in.Title, Description: in.Description, StartsAt: in.StartsAt, EndsAt: in.EndsAt,
		Genre: in.Genre, PriceFrom: priceParam(in.PriceFrom), PosterKey: posterParam(in.PosterKey),
		ID: id, OrganizerID: organizerID, VenueID: in.VenueID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Event{}, badVenue()
	}
	if dup := slugTaken(err); dup != nil {
		return dbgen.Event{}, dup
	}
	if rangeErr := endsBeforeStarts(err); rangeErr != nil {
		return dbgen.Event{}, rangeErr
	}
	if err != nil {
		return dbgen.Event{}, fmt.Errorf("update event: %w", err)
	}
	return e, nil
}

func (s *Service) SetStatus(ctx context.Context, organizerID, id int64, status string) (dbgen.Event, error) {
	switch status {
	case StatusDraft, StatusPublished, StatusCancelled:
	default:
		return dbgen.Event{}, ErrBadStatus
	}
	e, err := s.q.SetEventStatus(ctx, dbgen.SetEventStatusParams{Status: status, ID: id, OrganizerID: organizerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Event{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Event{}, fmt.Errorf("set event status: %w", err)
	}
	return e, nil
}

func (s *Service) Delete(ctx context.Context, organizerID, id int64) error {
	n, err := s.q.DeleteEvent(ctx, dbgen.DeleteEventParams{ID: id, OrganizerID: organizerID})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return ErrHasTickets
	}
	if err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func badVenue() error {
	return &ValidationError{Fields: map[string]string{FieldVenue: CodeNotFound}}
}

func slugTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == slugConstraint {
		return &ValidationError{Fields: map[string]string{FieldSlug: CodeTaken}}
	}
	return nil
}

func endsBeforeStarts(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolation && pgErr.ConstraintName == endsAfterStart {
		return &ValidationError{Fields: map[string]string{FieldEndsAt: CodeRange}}
	}
	return nil
}

func priceParam(p *int64) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *p, Valid: true}
}

func posterParam(key string) pgtype.Text {
	if key == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: key, Valid: true}
}

func normalize(in Input) Input {
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Genre = strings.ToLower(strings.TrimSpace(in.Genre))
	return in
}

func validate(in Input) error {
	fields := map[string]string{}
	if in.VenueID == 0 {
		fields[FieldVenue] = CodeRequired
	}
	switch n := len(in.Slug); {
	case n == 0:
		fields[FieldSlug] = CodeRequired
	case !slugPattern.MatchString(in.Slug):
		fields[FieldSlug] = CodeFormat
	case n < slugMinLen || n > slugMaxLen:
		fields[FieldSlug] = CodeLength
	}
	switch n := utf8.RuneCountInString(in.Title); {
	case n == 0:
		fields[FieldTitle] = CodeRequired
	case n > titleMaxLen:
		fields[FieldTitle] = CodeLength
	}
	if in.StartsAt.IsZero() {
		fields[FieldStartsAt] = CodeRequired
	}
	switch {
	case in.EndsAt.IsZero():
		fields[FieldEndsAt] = CodeRequired
	case !in.StartsAt.IsZero() && !in.EndsAt.After(in.StartsAt):
		fields[FieldEndsAt] = CodeRange
	}
	if !IsGenre(in.Genre) {
		fields[FieldGenre] = CodeInvalid
	}
	if in.PriceFrom != nil && *in.PriceFrom < 0 {
		fields[FieldPriceFrom] = CodeRange
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func CheckInput(in Input) error {
	return validate(normalize(in))
}
