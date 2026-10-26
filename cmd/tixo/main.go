package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
)

var version = "dev"

type command struct {
	summary string
	run     func(ctx context.Context, args []string, stdout io.Writer) error
}

var commands = map[string]command{
	"version": {
		summary: "print build version",
		run: func(_ context.Context, _ []string, stdout io.Writer) error {
			_, err := fmt.Fprintln(stdout, version)
			return err
		},
	},
}

var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "tixo:", err)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errUsage
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return errUsage
	}
	return cmd.run(ctx, args[1:], stdout)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: tixo <command> [flags]")
	fmt.Fprintln(w, "\nCommands:")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-12s %s\n", name, commands[name].summary)
	}
}
