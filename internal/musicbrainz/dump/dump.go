package dump

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ArtistEntry = "mbdump/artist"

	artistColumns = 19
	colGID        = 1
	colName       = 2
	colSortName   = 3
	colType       = 10
	colComment    = 13
)

var artistTypes = map[int]string{
	1: "Person",
	2: "Group",
	3: "Other",
	4: "Character",
	5: "Orchestra",
	6: "Choir",
}

var bzip2Magic = []byte("BZh")

type Artist struct {
	MBID           [16]byte
	Name           string
	SortName       string
	Type           string
	Disambiguation string
}

func Open(path string) (io.ReadCloser, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	magic, err := br.Peek(len(bzip2Magic))
	if err != nil && !errors.Is(err, io.EOF) {
		_ = f.Close()
		return nil, err
	}
	if !bytes.Equal(magic, bzip2Magic) {
		return readCloser{Reader: br, Closer: f}, nil
	}

	tr := tar.NewReader(bzip2.NewReader(br))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			_ = f.Close()
			return nil, fmt.Errorf("%s: archive has no %s entry", path, ArtistEntry)
		}
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%s: read archive: %w", path, err)
		}
		if hdr.Name == ArtistEntry && hdr.Typeflag == tar.TypeReg {
			return readCloser{Reader: tr, Closer: f}, nil
		}
	}
}

type readCloser struct {
	io.Reader
	io.Closer
}

type Reader struct {
	r    *bufio.Reader
	line int
}

func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 1<<20)}
}

func (r *Reader) Line() int { return r.line }

func (r *Reader) Next() (Artist, error) {
	raw, err := r.r.ReadString('\n')
	if errors.Is(err, io.EOF) && raw == "" {
		return Artist{}, io.EOF
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return Artist{}, fmt.Errorf("line %d: %w", r.line+1, err)
	}
	r.line++
	a, err := parseArtist(strings.TrimSuffix(raw, "\n"))
	if err != nil {
		return Artist{}, fmt.Errorf("line %d: %w", r.line, err)
	}
	return a, nil
}

func parseArtist(line string) (Artist, error) {
	fields := strings.Split(line, "\t")
	if len(fields) != artistColumns {
		return Artist{}, fmt.Errorf("expected %d columns, got %d; the dump format may have changed", artistColumns, len(fields))
	}

	gid, ok, err := field(fields[colGID])
	if err != nil {
		return Artist{}, fmt.Errorf("gid: %w", err)
	}
	if !ok {
		return Artist{}, errors.New("gid is NULL")
	}
	mbid, err := parseUUID(gid)
	if err != nil {
		return Artist{}, err
	}

	var a Artist
	a.MBID = mbid
	if a.Name, ok, err = field(fields[colName]); err != nil {
		return Artist{}, fmt.Errorf("name: %w", err)
	}
	if !ok || a.Name == "" {
		return Artist{}, errors.New("name is empty")
	}
	if a.SortName, _, err = field(fields[colSortName]); err != nil {
		return Artist{}, fmt.Errorf("sort_name: %w", err)
	}
	if a.Disambiguation, _, err = field(fields[colComment]); err != nil {
		return Artist{}, fmt.Errorf("comment: %w", err)
	}

	typ, ok, err := field(fields[colType])
	if err != nil {
		return Artist{}, fmt.Errorf("type: %w", err)
	}
	if ok {
		n, err := strconv.Atoi(typ)
		if err != nil {
			return Artist{}, fmt.Errorf("type: %q is not a number", typ)
		}
		a.Type = artistTypes[n]
	}
	return a, nil
}

func field(s string) (string, bool, error) {
	if s == `\N` {
		return "", false, nil
	}
	v, err := Unescape(s)
	return v, true, err
}

func Unescape(s string) (string, error) {
	if !strings.Contains(s, `\`) {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i == len(s) {
			return "", errors.New("trailing backslash")
		}
		switch c = s[i]; c {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x':
			j := i + 1
			for j < len(s) && j < i+3 && isHex(s[j]) {
				j++
			}
			if j == i+1 {
				b.WriteByte('x')
				continue
			}
			var v byte
			for _, h := range []byte(s[i+1 : j]) {
				v = v<<4 | hexValue(h)
			}
			b.WriteByte(v)
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			var v byte
			for _, o := range []byte(s[i:j]) {
				v = v<<3 | (o - '0')
			}
			b.WriteByte(v)
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), nil
}

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func parseUUID(s string) ([16]byte, error) {
	var out [16]byte
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return out, fmt.Errorf("gid %q is not a UUID", s)
	}
	h := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(out[:], []byte(h)); err != nil {
		return out, fmt.Errorf("gid %q is not a UUID", s)
	}
	return out, nil
}
