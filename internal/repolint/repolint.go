package repolint

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"strings"
)

type Violation struct {
	Path string
	Line int
	Text string
}

var skipDirs = map[string]bool{
	".git":            true,
	"node_modules":    true,
	"vendor":          true,
	"bin":             true,
	"tmp":             true,
	"internal/db/gen": true,
}

var hashCommentNames = map[string]bool{
	"Dockerfile":    true,
	"Makefile":      true,
	".gitignore":    true,
	".dockerignore": true,
}

var hashCommentExts = map[string]bool{".yml": true, ".yaml": true, ".toml": true}

func Check(fsys fs.FS) ([]Violation, error) {
	var out []Violation
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[p] || skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		check := checkerFor(p)
		if check == nil {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out = append(out, check(p, data)...)
		return nil
	})
	return out, err
}

func checkerFor(p string) func(string, []byte) []Violation {
	name := path.Base(p)
	switch ext := path.Ext(name); {
	case ext == ".go":
		return checkGo
	case ext == ".sql":
		return lines(sqlComment)
	case ext == ".html":
		return lines(htmlComment)
	case ext == ".css":
		return lines(func(l string) bool { return strings.Contains(l, "/*") })
	case ext == ".js":
		return lines(jsComment)
	case hashCommentExts[ext], hashCommentNames[name], strings.HasPrefix(name, ".env"):
		return lines(hashComment)
	}
	return nil
}

func checkGo(p string, data []byte) []Violation {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, data, parser.ParseComments)
	if err != nil || ast.IsGenerated(f) {
		return nil
	}
	var out []Violation
	for _, group := range f.Comments {
		for _, c := range group.List {
			if strings.HasPrefix(c.Text, "//go:") {
				continue
			}
			out = append(out, Violation{Path: p, Line: fset.Position(c.Pos()).Line, Text: c.Text})
		}
	}
	return out
}

func lines(isComment func(string) bool) func(string, []byte) []Violation {
	return func(p string, data []byte) []Violation {
		var out []Violation
		sc := bufio.NewScanner(bytes.NewReader(data))
		for n := 1; sc.Scan(); n++ {
			if isComment(sc.Text()) {
				out = append(out, Violation{Path: p, Line: n, Text: sc.Text()})
			}
		}
		return out
	}
}

func sqlComment(l string) bool {
	t := strings.TrimSpace(l)
	if !strings.HasPrefix(t, "--") {
		return false
	}
	return !strings.HasPrefix(t, "-- +goose ") && !strings.HasPrefix(t, "-- name: ")
}

func htmlComment(l string) bool {
	return strings.Contains(l, "<!--") || strings.Contains(l, "{{/*") || strings.Contains(l, "{{- /*")
}

func jsComment(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "//") || strings.Contains(t, "/*")
}

func hashComment(l string) bool {
	return strings.HasPrefix(strings.TrimSpace(l), "#")
}
