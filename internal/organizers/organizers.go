package organizers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	RoleOwner  = "owner"
	RoleEditor = "editor"

	FieldName = "name"
	FieldSlug = "slug"

	CodeRequired = "required"
	CodeLength   = "length"
	CodeFormat   = "format"
	CodeReserved = "reserved"
	CodeTaken    = "taken"

	nameMinLen = 2
	nameMaxLen = 100
	slugMinLen = 3
	slugMaxLen = 50

	eventsPageSize  = 50
	uniqueViolation = "23505"
	slugConstraint  = "organizers_slug_key"
)

var (
	ErrNotFound = errors.New("organizer not found")

	slugPattern   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reservedSlugs = []string{"new"}
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
	return "invalid organizer: " + strings.Join(parts, ", ")
}

type CreateInput struct {
	Name string
	Slug string
}

type Membership struct {
	Organizer dbgen.Organizer
	Role      string
}

type Service struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: dbgen.New(pool)}
}

func (s *Service) Create(ctx context.Context, userID int64, in CreateInput) (dbgen.Organizer, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	if err := validate(in); err != nil {
		return dbgen.Organizer{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return dbgen.Organizer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	org, err := q.CreateOrganizer(ctx, dbgen.CreateOrganizerParams{Slug: in.Slug, Name: in.Name, Contacts: json.RawMessage(`{}`)})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == slugConstraint {
		return dbgen.Organizer{}, &ValidationError{Fields: map[string]string{FieldSlug: CodeTaken}}
	}
	if err != nil {
		return dbgen.Organizer{}, fmt.Errorf("create organizer: %w", err)
	}
	if _, err := q.AddMembership(ctx, dbgen.AddMembershipParams{UserID: userID, OrganizerID: org.ID, Role: RoleOwner}); err != nil {
		return dbgen.Organizer{}, fmt.Errorf("add owner: %w", err)
	}
	return org, tx.Commit(ctx)
}

func validate(in CreateInput) error {
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		fields[FieldName] = CodeRequired
	case n < nameMinLen || n > nameMaxLen:
		fields[FieldName] = CodeLength
	}
	switch n := len(in.Slug); {
	case n == 0:
		fields[FieldSlug] = CodeRequired
	case !slugPattern.MatchString(in.Slug):
		fields[FieldSlug] = CodeFormat
	case n < slugMinLen || n > slugMaxLen:
		fields[FieldSlug] = CodeLength
	case slices.Contains(reservedSlugs, in.Slug):
		fields[FieldSlug] = CodeReserved
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func (s *Service) ListForUser(ctx context.Context, userID int64) ([]Membership, error) {
	rows, err := s.q.ListOrganizersForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Membership, len(rows))
	for i, r := range rows {
		out[i] = Membership{Organizer: r.Organizer, Role: r.Role}
	}
	return out, nil
}

func (s *Service) ForMember(ctx context.Context, userID int64, slug string) (Membership, error) {
	row, err := s.q.GetOrganizerForMember(ctx, dbgen.GetOrganizerForMemberParams{Slug: slug, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, err
	}
	return Membership{Organizer: row.Organizer, Role: row.Role}, nil
}

func (s *Service) Events(ctx context.Context, m Membership) ([]dbgen.Event, error) {
	return s.q.ListEventsForOrganizer(ctx, dbgen.ListEventsForOrganizerParams{OrganizerID: m.Organizer.ID, Lim: eventsPageSize})
}
