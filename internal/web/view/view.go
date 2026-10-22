package view

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/web/i18n"
)

type Config struct {
	BaseURL url.URL
	Logger  *slog.Logger
	SignIn  bool
}

type Viewer struct {
	Name  string
	Email string
}

type viewerKey struct{}

type flashKey struct{}

func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, &v)
}

func WithFlash(ctx context.Context, pop func() string) context.Context {
	return context.WithValue(ctx, flashKey{}, pop)
}

type Renderer struct {
	pages map[string]*template.Template
	tr    *i18n.Translator
	cfg   Config
}

func New(fsys fs.FS, tr *i18n.Translator, cfg Config) (*Renderer, error) {
	shared, err := template.ParseFS(fsys, "templates/layouts/*.html", "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("view: parse layouts: %w", err)
	}
	pageFiles, err := pageFiles(fsys)
	if err != nil {
		return nil, err
	}
	v := &Renderer{pages: make(map[string]*template.Template, len(pageFiles)), tr: tr, cfg: cfg}
	for _, file := range pageFiles {
		clone, err := shared.Clone()
		if err != nil {
			return nil, fmt.Errorf("view: clone layouts: %w", err)
		}
		tmpl, err := clone.ParseFS(fsys, file)
		if err != nil {
			return nil, fmt.Errorf("view: parse %s: %w", file, err)
		}
		name := strings.TrimSuffix(strings.TrimPrefix(file, "templates/"), ".html")
		v.pages[name] = tmpl
	}
	return v, nil
}

func pageFiles(fsys fs.FS) ([]string, error) {
	var files []string
	for _, dir := range []string{"templates/pages", "templates/errors"} {
		matches, err := fs.Glob(fsys, dir+"/*.html")
		if err != nil {
			return nil, fmt.Errorf("view: glob %s: %w", dir, err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("view: no page templates found")
	}
	return files, nil
}

func (v *Renderer) Render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	tmpl, ok := v.pages[page]
	if !ok {
		v.fail(w, r, fmt.Errorf("view: unknown page %q", page))
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", v.page(r, page, data)); err != nil {
		v.fail(w, r, fmt.Errorf("view: render %s: %w", page, err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (v *Renderer) NotFound(w http.ResponseWriter, r *http.Request) {
	v.Render(w, r, http.StatusNotFound, "errors/404", nil)
}

func (v *Renderer) ServerError(w http.ResponseWriter, r *http.Request) {
	v.Render(w, r, http.StatusInternalServerError, "errors/500", nil)
}

func (v *Renderer) fail(w http.ResponseWriter, r *http.Request, err error) {
	v.cfg.Logger.ErrorContext(r.Context(), "render failed", "err", err, "request_id", middleware.GetReqID(r.Context()))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (v *Renderer) page(r *http.Request, name string, data any) *Page {
	lang := i18n.FromContext(r.Context())
	viewer, _ := r.Context().Value(viewerKey{}).(*Viewer)
	pop, _ := r.Context().Value(flashKey{}).(func() string)
	return &Page{
		Viewer:         viewer,
		SignIn:         v.cfg.SignIn,
		popFlash:       pop,
		Name:           name,
		Lang:           lang,
		Path:           cleanPath(r.URL.Path),
		Data:           data,
		SuggestEnglish: i18n.SuggestEnglish(r, lang),
		RequestID:      middleware.GetReqID(r.Context()),
		Year:           time.Now().Year(),
		tr:             v.tr,
		base:           v.cfg.BaseURL,
	}
}

type Page struct {
	Viewer         *Viewer
	SignIn         bool
	Name           string
	Lang           i18n.Lang
	Path           string
	Data           any
	SuggestEnglish bool
	RequestID      string
	Year           int
	tr             *i18n.Translator
	base           url.URL
	popFlash       func() string
	flash          *string
}

func (p *Page) Flash() string {
	if p.flash == nil {
		var msg string
		if p.popFlash != nil {
			msg = p.popFlash()
		}
		p.flash = &msg
	}
	return *p.flash
}

type Alternate struct {
	Hreflang string
	Href     string
}

func (p *Page) T(id string, kv ...any) (string, error) { return p.tr.T(p.Lang, id, kv...) }

func (p *Page) N(id string, count int) (string, error) { return p.tr.N(p.Lang, id, count) }

func (p *Page) URL(target string) string { return i18n.Path(p.Lang, cleanPath(target)) }

func (p *Page) SwitchURL() string { return i18n.Path(p.OtherLang(), p.Path) }

func (p *Page) EnglishURL() string { return i18n.Path(i18n.EN, p.Path) }

func (p *Page) OtherLang() i18n.Lang {
	if p.Lang == i18n.EN {
		return i18n.UK
	}
	return i18n.EN
}

func (p *Page) Canonical() string { return p.absolute(i18n.Path(p.Lang, p.Path)) }

func (p *Page) Alternates() []Alternate {
	alts := make([]Alternate, 0, len(i18n.Supported)+1)
	for _, lang := range i18n.Supported {
		alts = append(alts, Alternate{Hreflang: string(lang), Href: p.absolute(i18n.Path(lang, p.Path))})
	}
	return append(alts, Alternate{Hreflang: "x-default", Href: p.absolute(i18n.Path(i18n.Default, p.Path))})
}

func (p *Page) absolute(localPath string) string {
	u := p.base
	u.Path = localPath
	return u.String()
}

func cleanPath(target string) string {
	if target == "" || target == "/" {
		return "/"
	}
	cleaned := path.Clean("/" + target)
	if strings.HasSuffix(target, "/") {
		cleaned += "/"
	}
	return cleaned
}
