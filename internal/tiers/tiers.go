package tiers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	FieldName     = "name"
	FieldPrice    = "price"
	FieldQuantity = "quantity"

	CodeRequired = "required"
	CodeLength   = "length"
	CodeRange    = "range"
	CodeTaken    = "taken"
	CodeNotFound = "not_found"

	nameMinLen  = 2
	nameMaxLen  = 100
	minQuantity = 1
	maxQuantity = 1_000_000

	uniqueViolation = "23505"
	checkViolation  = "23514"
	nameConstraint  = "ticket_tiers_event_name_key"
	availableRange  = "ticket_tiers_available_range"
)

var (
	ErrNotFound = errors.New("ticket tier not found")
	ErrSold     = errors.New("ticket tier has sold tickets")
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
	return "invalid ticket tier: " + strings.Join(parts, ", ")
}

type Input struct {
	Name     string
	Price    int64
	Quantity int32
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) List(ctx context.Context, organizerID, eventID int64) ([]dbgen.TicketTier, error) {
	return s.q.ListTicketTiersForOrganizer(ctx, dbgen.ListTicketTiersForOrganizerParams{EventID: eventID, OrganizerID: organizerID})
}

func (s *Service) Get(ctx context.Context, organizerID, id int64) (dbgen.TicketTier, error) {
	t, err := s.q.GetTicketTier(ctx, dbgen.GetTicketTierParams{ID: id, OrganizerID: organizerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.TicketTier{}, ErrNotFound
	}
	if err != nil {
		return dbgen.TicketTier{}, fmt.Errorf("get ticket tier: %w", err)
	}
	return t, nil
}

func CheckInput(in Input) error {
	return validate(normalize(in))
}

func (s *Service) Create(ctx context.Context, organizerID, eventID int64, in Input) (dbgen.TicketTier, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.TicketTier{}, err
	}
	t, err := s.q.CreateTicketTier(ctx, dbgen.CreateTicketTierParams{
		Name: in.Name, Price: in.Price, Quantity: in.Quantity, EventID: eventID, OrganizerID: organizerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.TicketTier{}, ErrNotFound
	}
	if dup := nameTaken(err); dup != nil {
		return dbgen.TicketTier{}, dup
	}
	if err != nil {
		return dbgen.TicketTier{}, fmt.Errorf("create ticket tier: %w", err)
	}
	return t, nil
}

func (s *Service) Update(ctx context.Context, organizerID, id int64, in Input) (dbgen.TicketTier, error) {
	if _, err := s.Get(ctx, organizerID, id); err != nil {
		return dbgen.TicketTier{}, err
	}
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.TicketTier{}, err
	}
	t, err := s.q.UpdateTicketTier(ctx, dbgen.UpdateTicketTierParams{
		Name: in.Name, Price: in.Price, Quantity: in.Quantity, ID: id, OrganizerID: organizerID,
	})
	if dup := nameTaken(err); dup != nil {
		return dbgen.TicketTier{}, dup
	}
	if quantityErr := quantityTooLow(err); quantityErr != nil {
		return dbgen.TicketTier{}, quantityErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.TicketTier{}, ErrNotFound
	}
	if err != nil {
		return dbgen.TicketTier{}, fmt.Errorf("update ticket tier: %w", err)
	}
	return t, nil
}

func (s *Service) Delete(ctx context.Context, organizerID, id int64) error {
	if _, err := s.Get(ctx, organizerID, id); err != nil {
		return err
	}
	n, err := s.q.DeleteUnsoldTicketTier(ctx, dbgen.DeleteUnsoldTicketTierParams{ID: id, OrganizerID: organizerID})
	if err != nil {
		return fmt.Errorf("delete ticket tier: %w", err)
	}
	if n == 0 {
		return ErrSold
	}
	return nil
}

func nameTaken(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == nameConstraint {
		return &ValidationError{Fields: map[string]string{FieldName: CodeTaken}}
	}
	return nil
}

func quantityTooLow(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolation && pgErr.ConstraintName == availableRange {
		return &ValidationError{Fields: map[string]string{FieldQuantity: CodeRange}}
	}
	return nil
}

func normalize(in Input) Input {
	in.Name = strings.TrimSpace(in.Name)
	return in
}

func validate(in Input) error {
	fields := map[string]string{}
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		fields[FieldName] = CodeRequired
	case n < nameMinLen || n > nameMaxLen:
		fields[FieldName] = CodeLength
	}
	if in.Price < 0 {
		fields[FieldPrice] = CodeRange
	}
	if in.Quantity < minQuantity || in.Quantity > maxQuantity {
		fields[FieldQuantity] = CodeRange
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}
