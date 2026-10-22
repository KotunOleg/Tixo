package importer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	"github.com/KotunOleg/Tixo/internal/musicbrainz/dump"
)

const radiohead = "a74b1b7f-71a5-4011-9441-d0b5e4122711"

func row(gid, name, typ string) string {
	cols := make([]string, 19)
	for i := range cols {
		cols[i] = `\N`
	}
	cols[0], cols[1], cols[2], cols[3], cols[10], cols[13] = "1", gid, name, name, typ, ""
	return strings.Join(cols, "\t") + "\n"
}

func importArchive(t *testing.T, pool *pgxpool.Pool) Report {
	t.Helper()
	rc, err := dump.Open("../dump/testdata/mbdump-sample.tar.bz2")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	rep, err := Import(context.Background(), pool, rc)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return rep
}

func artistCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM artists").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportTwiceDoesNotDuplicateArtists(t *testing.T) {
	pool := dbtest.Pool(t)

	first := importArchive(t, pool)
	if first.Read != 3 || first.Inserted != 3 || first.Updated != 0 {
		t.Errorf("first import = %v, want 3 read and 3 inserted", first)
	}
	second := importArchive(t, pool)
	if second.Read != 3 || second.Inserted != 0 || second.Updated != 0 || second.Unchanged != 3 {
		t.Errorf("second import = %v, want 3 unchanged", second)
	}
	if n := artistCount(t, pool); n != 3 {
		t.Errorf("artists after two imports = %d, want 3", n)
	}
}

func TestReimportUpdatesDumpFieldsAndKeepsEnrichment(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	importArchive(t, pool)

	_, err := pool.Exec(ctx, `UPDATE artists SET country = 'GB', genres = '{rock}', links = '{"wiki":"x"}', enriched_at = now() WHERE mbid = $1`, radiohead)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := Import(ctx, pool, strings.NewReader(row(radiohead, "Radiohead (renamed)", "1")))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Updated != 1 || rep.Inserted != 0 {
		t.Errorf("report = %v, want 1 updated", rep)
	}

	var name, typ, country string
	var genres []string
	var enriched bool
	err = pool.QueryRow(ctx, `SELECT name, type, country, genres, enriched_at IS NOT NULL FROM artists WHERE mbid = $1`, radiohead).
		Scan(&name, &typ, &country, &genres, &enriched)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Radiohead (renamed)" || typ != "Person" {
		t.Errorf("dump fields = %q, %q; want the new name and type", name, typ)
	}
	if country != "GB" || len(genres) != 1 || !enriched {
		t.Errorf("enrichment lost: country=%q genres=%v enriched=%v", country, genres, enriched)
	}
}

func TestImportKeepsArtistAddedBySeedUnderTheSameMBID(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	var id int64
	err := pool.QueryRow(ctx, `INSERT INTO artists (mbid, name) VALUES ($1, 'Radiohead') RETURNING id`, radiohead).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	importArchive(t, pool)

	var after int64
	if err := pool.QueryRow(ctx, `SELECT id FROM artists WHERE mbid = $1`, radiohead).Scan(&after); err != nil || after != id {
		t.Errorf("seeded artist id = %d, after import = %d (%v); a lineup referencing it would break", id, after, err)
	}
	if n := artistCount(t, pool); n != 3 {
		t.Errorf("artists = %d, want 3", n)
	}
}

func TestBrokenRowRollsBackWholeImport(t *testing.T) {
	pool := dbtest.Pool(t)
	input := row(radiohead, "Radiohead", "2") +
		"1\tonly-two-columns\n" +
		row("b8a7c51f-362c-4dcb-a259-bc6e0095f0a6", "Never imported", "1")

	_, err := Import(context.Background(), pool, strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want an error pointing at line 2", err)
	}
	if n := artistCount(t, pool); n != 0 {
		t.Errorf("artists after failed import = %d, want 0", n)
	}
}

func TestSecondConcurrentImportIsRefused(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		t.Fatal(err)
	}

	_, err = Import(ctx, pool, strings.NewReader(row(radiohead, "Radiohead", "2")))
	if !errors.Is(err, ErrImportRunning) {
		t.Errorf("err = %v, want ErrImportRunning", err)
	}
}
