package controllers

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/KotunOleg/Tixo/internal/web/view"
)

func Mount(r chi.Router, v *view.Renderer, fsys fs.FS) {
	r.Get("/", Home{View: v}.ServeHTTP)
	r.Get("/static/*", staticFiles(v, fsys))
}

func staticFiles(v *view.Renderer, fsys fs.FS) http.HandlerFunc {
	static, err := fs.Sub(fsys, "static")
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
		info, err := fs.Stat(static, name)
		if name == "" || err != nil || info.IsDir() {
			v.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFileFS(w, r, static, name)
	}
}
