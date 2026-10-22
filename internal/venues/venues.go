package venues

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
	FieldAddress  = "address"
	FieldCity     = "city"
	FieldLat      = "lat"
	FieldLng      = "lng"
	FieldCapacity = "capacity"

	CodeRequired = "required"
	CodeLength   = "length"
	CodeFormat   = "format"
	CodeRange    = "range"
	CodeTaken    = "taken"

	nameMinLen    = 2
	nameMaxLen    = 150
	addressMaxLen = 200
	cityMinLen    = 2
	cityMaxLen    = 100
	minCapacity   = 1
	maxCapacity   = 1_000_000

	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
	nameConstraint      = "venues_organizer_name_key"
)

var (
	ErrNotFound  = errors.New("venue not found")
	ErrHasEvents = errors.New("venue has events and cannot be deleted")
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
	return "invalid venue: " + strings.Join(parts, ", ")
}

type Input struct {
	Name     string
	Address  string
	City     string
	Lat      float64
	Lng      float64
	Capacity int32
}

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) List(ctx context.Context, organizerID int64) ([]dbgen.Venue, error) {
	return s.q.ListVenuesForOrganizer(ctx, organizerID)
}

func (s *Service) Get(ctx context.Context, organizerID, id int64) (dbgen.Venue, error) {
	v, err := s.q.GetVenue(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Venue{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Venue{}, fmt.Errorf("get venue: %w", err)
	}
	if !v.OrganizerID.Valid || v.OrganizerID.Int64 != organizerID {
		return dbgen.Venue{}, ErrNotFound
	}
	return v, nil
}

func CheckInput(in Input) error {
	return validate(normalize(in))
}

func (s *Service) Create(ctx context.Context, organizerID int64, in Input) (dbgen.Venue, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.Venue{}, err
	}
	v, err := s.q.CreateVenue(ctx, dbgen.CreateVenueParams{
		OrganizerID: organizerID, Name: in.Name, Address: in.Address, City: in.City, Lat: in.Lat, Lng: in.Lng, Capacity: in.Capacity,
	})
	if dup := nameTaken(err); dup != nil {
		return dbgen.Venue{}, dup
	}
	if err != nil {
		return dbgen.Venue{}, fmt.Errorf("create venue: %w", err)
	}
	return v, nil
}

func (s *Service) Update(ctx context.Context, organizerID, id int64, in Input) (dbgen.Venue, error) {
	in = normalize(in)
	if err := validate(in); err != nil {
		return dbgen.Venue{}, err
	}
	v, err := s.q.UpdateVenue(ctx, dbgen.UpdateVenueParams{
		Name: in.Name, Address: in.Address, City: in.City, Lat: in.Lat, Lng: in.Lng, Capacity: in.Capacity, ID: id, OrganizerID: organizerID,
	})
	if dup := nameTaken(err); dup != nil {
		return dbgen.Venue{}, dup
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Venue{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Venue{}, fmt.Errorf("update venue: %w", err)
	}
	return v, nil
}

func (s *Service) Delete(ctx context.Context, organizerID, id int64) error {
	n, err := s.q.DeleteVenue(ctx, dbgen.DeleteVenueParams{ID: id, OrganizerID: organizerID})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return ErrHasEvents
	}
	if err != nil {
		return fmt.Errorf("delete venue: %w", err)
	}
	if n == 0 {
		return ErrNotFound
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

func normalize(in Input) Input {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	in.City = strings.TrimSpace(in.City)
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
	switch n := utf8.RuneCountInString(in.Address); {
	case n == 0:
		fields[FieldAddress] = CodeRequired
	case n > addressMaxLen:
		fields[FieldAddress] = CodeLength
	}
	switch n := utf8.RuneCountInString(in.City); {
	case n == 0:
		fields[FieldCity] = CodeRequired
	case n < cityMinLen || n > cityMaxLen:
		fields[FieldCity] = CodeLength
	}
	if in.Lat < -90 || in.Lat > 90 {
		fields[FieldLat] = CodeRange
	}
	if in.Lng < -180 || in.Lng > 180 {
		fields[FieldLng] = CodeRange
	}
	if in.Capacity < minCapacity || in.Capacity > maxCapacity {
		fields[FieldCapacity] = CodeRange
	}
	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}
