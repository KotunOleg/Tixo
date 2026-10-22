package artists

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	searchMinLen = 3
	searchLimit  = 20
	nameMaxLen   = 200
)

var (
	ErrNotFound    = errors.New("artist not found")
	ErrNameTooLong = errors.New("artist name too long")
)

type Service struct {
	q *dbgen.Queries
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{q: dbgen.New(pool)}
}

func (s *Service) Search(ctx context.Context, query string) ([]dbgen.Artist, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < searchMinLen {
		return nil, nil
	}
	rows, err := s.q.SearchArtists(ctx, dbgen.SearchArtistsParams{Query: query, Lim: searchLimit})
	if err != nil {
		return nil, fmt.Errorf("search artists: %w", err)
	}
	return rows, nil
}

func (s *Service) Get(ctx context.Context, id int64) (dbgen.Artist, error) {
	a, err := s.q.GetArtist(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Artist{}, ErrNotFound
	}
	if err != nil {
		return dbgen.Artist{}, fmt.Errorf("get artist: %w", err)
	}
	return a, nil
}

func (s *Service) Create(ctx context.Context, name string) (dbgen.Artist, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 {
		return dbgen.Artist{}, errors.New("artist name is required")
	} else if n > nameMaxLen {
		return dbgen.Artist{}, ErrNameTooLong
	}
	a, err := s.q.CreateArtist(ctx, dbgen.CreateArtistParams{Name: name, SortName: name})
	if err != nil {
		return dbgen.Artist{}, fmt.Errorf("create artist: %w", err)
	}
	return a, nil
}
