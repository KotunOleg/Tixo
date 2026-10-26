package lineup

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const foreignKeyViolation = "23503"

var (
	ErrEventNotFound  = errors.New("event not found")
	ErrArtistNotFound = errors.New("artist not found")
)

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) List(ctx context.Context, organizerID, eventID int64) ([]dbgen.ListEventArtistsForOrganizerRow, error) {
	rows, err := s.q.ListEventArtistsForOrganizer(ctx, dbgen.ListEventArtistsForOrganizerParams{EventID: eventID, OrganizerID: organizerID})
	if err != nil {
		return nil, fmt.Errorf("list event artists: %w", err)
	}
	return rows, nil
}

func (s *Service) Add(ctx context.Context, organizerID, eventID, artistID int64, headliner bool) error {
	count, err := s.q.CountEventArtists(ctx, eventID)
	if err != nil {
		return fmt.Errorf("count event artists: %w", err)
	}
	n, err := s.q.SetEventArtist(ctx, dbgen.SetEventArtistParams{
		ArtistID: artistID, Position: count, IsHeadliner: headliner, EventID: eventID, OrganizerID: organizerID,
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
		return ErrArtistNotFound
	}
	if err != nil {
		return fmt.Errorf("add event artist: %w", err)
	}
	if n == 0 {
		return ErrEventNotFound
	}
	return nil
}

func (s *Service) Remove(ctx context.Context, organizerID, eventID, artistID int64) error {
	if _, err := s.q.GetEventForOrganizer(ctx, dbgen.GetEventForOrganizerParams{ID: eventID, OrganizerID: organizerID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrEventNotFound
		}
		return fmt.Errorf("get event: %w", err)
	}
	if _, err := s.q.RemoveEventArtist(ctx, dbgen.RemoveEventArtistParams{EventID: eventID, OrganizerID: organizerID, ArtistID: artistID}); err != nil {
		return fmt.Errorf("remove event artist: %w", err)
	}
	return nil
}
