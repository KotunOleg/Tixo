package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestImportMusicBrainzNeedsDumpPath(t *testing.T) {
	for _, args := range [][]string{{"import-musicbrainz"}, {"import-musicbrainz", "a", "b"}} {
		err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{})
		if !errors.Is(err, errImportUsage) {
			t.Errorf("%v: err = %v, want usage error", args, err)
		}
	}
}
