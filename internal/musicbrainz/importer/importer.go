package importer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/musicbrainz/dump"
)

const lockKey int64 = 0x4d42_4152_5449_5354

var ErrImportRunning = errors.New("another musicbrainz import is running")

type Report struct {
	Read      int64
	Inserted  int64
	Updated   int64
	Unchanged int64
	Duration  time.Duration
}

func (r Report) String() string {
	return fmt.Sprintf("read %d, inserted %d, updated %d, unchanged %d in %s",
		r.Read, r.Inserted, r.Updated, r.Unchanged, r.Duration.Round(time.Millisecond))
}

const createStaging = `
CREATE TEMP TABLE artist_import (
    mbid           UUID NOT NULL,
    name           TEXT NOT NULL,
    sort_name      TEXT NOT NULL,
    type           TEXT,
    disambiguation TEXT NOT NULL
) ON COMMIT DROP`

const upsert = `
WITH up AS (
    INSERT INTO artists (mbid, name, sort_name, type, disambiguation)
    SELECT DISTINCT ON (mbid) mbid, name, sort_name, type, disambiguation
    FROM artist_import
    ORDER BY mbid
    ON CONFLICT (mbid) DO UPDATE
    SET name = EXCLUDED.name,
        sort_name = EXCLUDED.sort_name,
        type = EXCLUDED.type,
        disambiguation = EXCLUDED.disambiguation
    WHERE (artists.name, artists.sort_name, artists.type, artists.disambiguation)
          IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.sort_name, EXCLUDED.type, EXCLUDED.disambiguation)
    RETURNING (xmax = 0) AS inserted
)
SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM up`

var stagingColumns = []string{"mbid", "name", "sort_name", "type", "disambiguation"}

func Import(ctx context.Context, pool *pgxpool.Pool, r io.Reader) (Report, error) {
	start := time.Now()
	var rep Report

	tx, err := pool.Begin(ctx)
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", lockKey).Scan(&locked); err != nil {
		return rep, err
	}
	if !locked {
		return rep, ErrImportRunning
	}

	if _, err := tx.Exec(ctx, createStaging); err != nil {
		return rep, fmt.Errorf("create staging table: %w", err)
	}

	src := &artistSource{r: dump.NewReader(r)}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"artist_import"}, stagingColumns, src); err != nil {
		if src.err != nil {
			return rep, fmt.Errorf("read dump: %w", src.err)
		}
		return rep, fmt.Errorf("copy into staging (around line %d): %w", src.r.Line(), err)
	}
	rep.Read = src.n

	if err := tx.QueryRow(ctx, upsert).Scan(&rep.Inserted, &rep.Updated); err != nil {
		return rep, fmt.Errorf("upsert artists: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rep, err
	}
	rep.Unchanged = rep.Read - rep.Inserted - rep.Updated
	rep.Duration = time.Since(start)
	return rep, nil
}

type artistSource struct {
	r   *dump.Reader
	cur dump.Artist
	n   int64
	err error
}

func (s *artistSource) Next() bool {
	a, err := s.r.Next()
	if errors.Is(err, io.EOF) {
		return false
	}
	if err != nil {
		s.err = err
		return false
	}
	s.cur = a
	s.n++
	return true
}

func (s *artistSource) Values() ([]any, error) {
	var typ any
	if s.cur.Type != "" {
		typ = s.cur.Type
	}
	return []any{s.cur.MBID, s.cur.Name, s.cur.SortName, typ, s.cur.Disambiguation}, nil
}

func (s *artistSource) Err() error { return s.err }
