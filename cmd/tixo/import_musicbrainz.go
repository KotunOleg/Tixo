package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/KotunOleg/Tixo/internal/config"
	"github.com/KotunOleg/Tixo/internal/musicbrainz/dump"
	"github.com/KotunOleg/Tixo/internal/musicbrainz/importer"
	"github.com/KotunOleg/Tixo/internal/platform/postgres"
)

var errImportUsage = errors.New("import-musicbrainz: expected one argument: path to mbdump.tar.bz2 or an extracted mbdump/artist file")

func init() {
	commands["import-musicbrainz"] = command{summary: "import artists from a MusicBrainz dump", run: runImportMusicBrainz}
}

func runImportMusicBrainz(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errImportUsage
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	src, err := dump.Open(args[0])
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	fmt.Fprintf(stdout, "importing artists from %s\n", args[0])
	rep, err := importer.Import(ctx, pool, src)
	if err != nil {
		return fmt.Errorf("import-musicbrainz: %w", err)
	}
	fmt.Fprintln(stdout, rep)
	return nil
}
