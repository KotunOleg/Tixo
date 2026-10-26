package dump

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleArchive = "testdata/mbdump-sample.tar.bz2"

func row(gid, name, typ, comment string) string {
	cols := make([]string, artistColumns)
	for i := range cols {
		cols[i] = `\N`
	}
	cols[0] = "1"
	cols[colGID] = gid
	cols[colName] = name
	cols[colSortName] = name
	cols[colType] = typ
	cols[colComment] = comment
	return strings.Join(cols, "\t") + "\n"
}

func readAll(t *testing.T, r io.Reader) ([]Artist, error) {
	t.Helper()
	dr := NewReader(r)
	var out []Artist
	for {
		a, err := dr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		out = append(out, a)
	}
}

func TestUnescapeCopyTextFormat(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`plain`, "plain"},
		{`a\tb`, "a\tb"},
		{`line\nbreak\r`, "line\nbreak\r"},
		{`back\\slash`, `back\slash`},
		{`\b\f\v`, "\b\f\v"},
		{`\101\102`, "AB"},
		{`\x41\x4a`, "AJ"},
		{`\xzz`, "xzz"},
		{`\q`, "q"},
		{`Sigur Rós`, "Sigur Rós"},
	}
	for _, c := range cases {
		got, err := Unescape(c.in)
		if err != nil || got != c.want {
			t.Errorf("Unescape(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := Unescape(`dangling\`); err == nil {
		t.Error("trailing backslash accepted")
	}
}

func TestParseArtistMapsColumns(t *testing.T) {
	cases := []struct {
		name string
		line string
		want Artist
	}{
		{
			name: "group with escaped tab and comment",
			line: row("a74b1b7f-71a5-4011-9441-d0b5e4122711", `Tab\tBand`, "2", `UK rock`),
			want: Artist{Name: "Tab\tBand", SortName: "Tab\tBand", Type: "Group", Disambiguation: "UK rock"},
		},
		{
			name: "null type and null comment",
			line: row("a74b1b7f-71a5-4011-9441-d0b5e4122711", "Солоха", `\N`, `\N`),
			want: Artist{Name: "Солоха", SortName: "Солоха"},
		},
		{
			name: "unknown type id is left empty",
			line: row("a74b1b7f-71a5-4011-9441-d0b5e4122711", "Future", "99", ""),
			want: Artist{Name: "Future", SortName: "Future"},
		},
	}
	for _, c := range cases {
		got, err := parseArtist(strings.TrimSuffix(c.line, "\n"))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		got.MBID = [16]byte{}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestParseArtistRejectsMalformedRows(t *testing.T) {
	good := "a74b1b7f-71a5-4011-9441-d0b5e4122711"
	cases := map[string]string{
		"one column missing": strings.Join(strings.Split(strings.TrimSuffix(row(good, "X", "1", ""), "\n"), "\t")[:artistColumns-1], "\t"),
		"extra column":       strings.TrimSuffix(row(good, "X", "1", ""), "\n") + "\textra",
		"gid not a uuid":     strings.TrimSuffix(row("not-a-uuid", "X", "1", ""), "\n"),
		"gid null":           strings.TrimSuffix(row(`\N`, "X", "1", ""), "\n"),
		"empty name":         strings.TrimSuffix(row(good, "", "1", ""), "\n"),
		"type not a number":  strings.TrimSuffix(row(good, "X", "Person", ""), "\n"),
	}
	for name, line := range cases {
		if _, err := parseArtist(line); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReaderReportsLineNumberOfBadRow(t *testing.T) {
	good := row("a74b1b7f-71a5-4011-9441-d0b5e4122711", "Ok", "1", "")
	input := good + good + "1\tbroken\n" + good
	got, err := readAll(t, strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "line 3") || !strings.Contains(err.Error(), "columns") {
		t.Errorf("err = %v, want a column-count error on line 3", err)
	}
	if len(got) != 2 {
		t.Errorf("rows before the error = %d, want 2", len(got))
	}
}

func TestReaderAcceptsMissingFinalNewline(t *testing.T) {
	input := strings.TrimSuffix(row("a74b1b7f-71a5-4011-9441-d0b5e4122711", "Last", "1", ""), "\n")
	got, err := readAll(t, strings.NewReader(input))
	if err != nil || len(got) != 1 || got[0].Name != "Last" {
		t.Errorf("got %+v, %v; want the single row", got, err)
	}
}

func TestOpenStreamsArtistEntryFromArchive(t *testing.T) {
	rc, err := Open(sampleArchive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := readAll(t, rc)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(got))
	for i, a := range got {
		names[i] = a.Name
	}
	if fmt.Sprint(names) != fmt.Sprint([]string{"Radiohead", "Tab\tName", "Безіменний"}) {
		t.Errorf("names from archive = %q", names)
	}
	if got[0].Type != "Group" || got[1].Type != "Person" || got[1].Disambiguation != `back\slash` {
		t.Errorf("fields from archive = %+v", got)
	}
	if fmt.Sprintf("%x", got[0].MBID) != "a74b1b7f71a540119441d0b5e4122711" {
		t.Errorf("mbid = %x", got[0].MBID)
	}
}

func TestOpenReadsExtractedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artist")
	if err := os.WriteFile(path, []byte(row("a74b1b7f-71a5-4011-9441-d0b5e4122711", "Plain", "1", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := readAll(t, rc)
	if err != nil || len(got) != 1 || got[0].Name != "Plain" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestOpenFailsOnArchiveWithoutArtistEntry(t *testing.T) {
	_, err := Open("testdata/mbdump-no-artist.tar.bz2")
	if err == nil || !strings.Contains(err.Error(), ArtistEntry) {
		t.Errorf("err = %v, want missing %s", err, ArtistEntry)
	}
}

func TestTruncatedArchiveFailsInsteadOfEndingQuietly(t *testing.T) {
	src, err := os.ReadFile(sampleArchive)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "truncated.tar.bz2")
	if err := os.WriteFile(path, src[:len(src)*2/3], 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := Open(path)
	if err != nil {
		return
	}
	defer func() { _ = rc.Close() }()
	if _, err := readAll(t, rc); err == nil {
		t.Error("truncated archive read to the end without an error")
	}
}
