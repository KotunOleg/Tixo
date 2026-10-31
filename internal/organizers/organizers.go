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

	"github.com/KotunOleg/Tixo/internal/crm"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	RoleOwner  = "owner"
	RoleEditor = "editor"

	FieldName            = "name"
	FieldSlug            = "slug"
	FieldEmail           = "email"
	FieldContactEmail    = "contact_email"
	FieldContactPhone    = "contact_phone"
	FieldContactTelegram = "contact_telegram"

	CodeRequired = "required"
	CodeLength   = "length"
	CodeFormat   = "format"
	CodeReserved = "reserved"
	CodeTaken    = "taken"
	CodeNotFound = "not_found"
	CodeMember   = "member"

	nameMinLen = 2
	nameMaxLen = 100
	slugMinLen = 3
	slugMaxLen = 50

	ContactKeyEmail    = "email"
	ContactKeyPhone    = "phone"
	ContactKeyTelegram = "telegram"

	eventsPageSize  = 50
	uniqueViolation = "23505"
	slugConstraint  = "organizers_slug_key"
	memberPKey      = "memberships_pkey"
)

var (
	ErrNotFound  = errors.New("organizer not found")
	ErrForbidden = errors.New("only the owner can manage members")

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

type UpdateInput struct {
	Name            string
	ContactEmail    string
	ContactPhone    string
	ContactTelegram string
}

type Membership struct {
	Organizer dbgen.Organizer
	Role      string
}

type Member struct {
	UserID int64
	Name   string
	Email  string
	Role   string
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
	payload, err := json.Marshal(crm.ContactEvent{UserID: userID, Kind: crm.RoleOrganizer})
	if err != nil {
		return dbgen.Organizer{}, fmt.Errorf("marshal crm contact event: %w", err)
	}
	if _, err := q.InsertOutbox(ctx, dbgen.InsertOutboxParams{Topic: crm.TopicCRMContact, Payload: payload}); err != nil {
		return dbgen.Organizer{}, fmt.Errorf("insert crm contact outbox row: %w", err)
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

func (s *Service) Update(ctx context.Context, org dbgen.Organizer, in UpdateInput) (dbgen.Organizer, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.ContactEmail = strings.TrimSpace(in.ContactEmail)
	in.ContactPhone = strings.TrimSpace(in.ContactPhone)
	in.ContactTelegram = strings.TrimSpace(in.ContactTelegram)
	if err := validateUpdate(in); err != nil {
		return dbgen.Organizer{}, err
	}

	contacts := map[string]string{}
	if in.ContactEmail != "" {
		contacts[ContactKeyEmail] = in.ContactEmail
	}
	if in.ContactPhone != "" {
		contacts[ContactKeyPhone] = in.ContactPhone
	}
	if in.ContactTelegram != "" {
		contacts[ContactKeyTelegram] = in.ContactTelegram
	}
	raw, err := json.Marshal(contacts)
	if err != nil {
		return dbgen.Organizer{}, fmt.Errorf("marshal contacts: %w", err)
	}

	updated, err := s.q.UpdateOrganizer(ctx, dbgen.UpdateOrganizerParams{ID: org.ID, Name: in.Name, Contacts: raw})
	if err != nil {
		return dbgen.Organizer{}, fmt.Errorf("update organizer: %w", err)
	}
	return updated, nil
}

func validateUpdate(in UpdateInput) error {
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		fields[FieldName] = CodeRequired
	case n < nameMinLen || n > nameMaxLen:
		fields[FieldName] = CodeLength
	}
	if in.ContactEmail != "" && !strings.Contains(in.ContactEmail, "@") {
		fields[FieldContactEmail] = CodeFormat
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func Contacts(org dbgen.Organizer) map[string]string {
	if len(org.Contacts) == 0 {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal(org.Contacts, &m)
	return m
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

func (s *Service) Members(ctx context.Context, m Membership) ([]Member, error) {
	rows, err := s.q.ListMembers(ctx, m.Organizer.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Member, len(rows))
	for i, r := range rows {
		out[i] = Member{UserID: r.ID, Name: r.Name, Email: r.Email, Role: r.Role}
	}
	return out, nil
}

func (s *Service) AddMember(ctx context.Context, actor Membership, email string) (Member, error) {
	if actor.Role != RoleOwner {
		return Member{}, ErrForbidden
	}
	email = strings.ToLower(strings.TrimSpace(email))
	switch {
	case email == "":
		return Member{}, emailError(CodeRequired)
	case !strings.Contains(email, "@"):
		return Member{}, emailError(CodeFormat)
	}
	u, err := s.q.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Member{}, emailError(CodeNotFound)
	}
	if err != nil {
		return Member{}, err
	}
	_, err = s.q.AddMembership(ctx, dbgen.AddMembershipParams{UserID: u.ID, OrganizerID: actor.Organizer.ID, Role: RoleEditor})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == memberPKey {
		return Member{}, emailError(CodeMember)
	}
	if err != nil {
		return Member{}, fmt.Errorf("add member: %w", err)
	}
	return Member{UserID: u.ID, Name: u.Name, Email: u.Email, Role: RoleEditor}, nil
}

func emailError(code string) error {
	return &ValidationError{Fields: map[string]string{FieldEmail: code}}
}
