package controllers

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/platform/storage"
	"github.com/KotunOleg/Tixo/internal/users"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type Deps struct {
	View       *view.Renderer
	FS         fs.FS
	Sessions   *scs.SessionManager
	Google     *auth.Google
	Users      *users.Service
	Organizers *organizers.Service
	Storage    *storage.Store
	Logger     *slog.Logger
}

func Mount(r chi.Router, d Deps) {
	r.Get("/static/*", staticFiles(d.View, d.FS))
	r.Get("/media/{key}", mediaFiles(d.View, d.Storage))
	r.Group(func(r chi.Router) {
		r.Use(d.Sessions.LoadAndSave, d.loadUser())
		r.Get("/", Home{View: d.View}.ServeHTTP)

		a := Auth{View: d.View, Sessions: d.Sessions, Google: d.Google, Users: d.Users, Log: d.Logger}
		r.Get("/auth/google/login", a.Login)
		r.Get(auth.CallbackPath, a.Callback)
		r.Post("/auth/logout", a.Logout)

		o := Organizers{View: d.View, Sessions: d.Sessions, Svc: d.Organizers, Log: d.Logger}
		r.Group(func(r chi.Router) {
			r.Use(requireUser)
			r.Get("/org", o.List)
			r.Get("/org/new", o.New)
			r.Post("/org/new", o.Create)
			r.With(o.RequireMember).Get("/org/{slug}", o.Show)
			r.With(o.RequireMember).Post("/org/{slug}/members", o.AddMember)
			r.With(o.RequireMember).Get("/org/{slug}/edit", o.Edit)
			r.With(o.RequireMember).Post("/org/{slug}/edit", o.EditSubmit)
		})
	})
}

func (d Deps) loadUser() func(http.Handler) http.Handler {
	return loadUser(d.Sessions, d.Users, d.Logger, d.View.ServerError)
}

func (d Deps) NotFound() http.HandlerFunc {
	return d.Sessions.LoadAndSave(d.loadUser()(http.HandlerFunc(d.View.NotFound))).ServeHTTP
}

func staticFiles(v *view.Renderer, fsys fs.FS) http.HandlerFunc {
	static, err := fs.Sub(fsys, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
		if !fs.ValidPath(name) || name == "." {
			v.NotFound(w, r)
			return
		}
		info, err := fs.Stat(static, name)
		if err != nil || info.IsDir() {
			v.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	}
}

func mediaFiles(v *view.Renderer, store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := store.Open(chi.URLParam(r, "key"))
		if errors.Is(err, storage.ErrNotFound) {
			v.NotFound(w, r)
			return
		}
		if err != nil {
			v.ServerError(w, r)
			return
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			v.ServerError(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}
