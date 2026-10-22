package controllers

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/organizers"
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
	Logger     *slog.Logger
}

func Mount(r chi.Router, d Deps) {
	r.Get("/static/*", staticFiles(d.View, d.FS))
	r.Group(func(r chi.Router) {
		r.Use(d.Sessions.LoadAndSave, d.loadUser())
		r.Get("/", Home{View: d.View}.ServeHTTP)

		a := Auth{View: d.View, Sessions: d.Sessions, Google: d.Google, Users: d.Users, Log: d.Logger}
		r.Get("/auth/google/login", a.Login)
		r.Get(auth.CallbackPath, a.Callback)
		r.Post("/auth/logout", a.Logout)

		o := Organizers{View: d.View, Svc: d.Organizers, Log: d.Logger}
		r.Group(func(r chi.Router) {
			r.Use(requireUser)
			r.Get("/org", o.List)
			r.Get("/org/new", o.New)
			r.Post("/org/new", o.Create)
			r.With(o.RequireMember).Get("/org/{slug}", o.Show)
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
