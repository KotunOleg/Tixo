package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/users"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

func loadUser(sm *scs.SessionManager, svc *users.Service, log *slog.Logger, onError http.HandlerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session := r.Context()
			ctx := view.WithFlash(session, func() string { return auth.PopFlash(session, sm) })
			ctx = view.WithPurchase(ctx, func() (string, bool) { return auth.PopPurchase(session, sm) })
			if id, ok := auth.UserID(session, sm); ok && svc != nil {
				u, err := svc.Get(session, id)
				switch {
				case errors.Is(err, users.ErrNotFound):
					auth.ForgetUser(session, sm)
				case err != nil:
					log.ErrorContext(session, "load signed-in user", "user_id", id, "error", err, "request_id", middleware.GetReqID(session))
					onError(w, r)
					return
				default:
					ctx = auth.WithUser(ctx, u)
					ctx = view.WithViewer(ctx, view.Viewer{Name: u.Name, Email: u.Email})
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.CurrentUser(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		target := r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		login := i18n.Path(i18n.FromContext(r.Context()), "/auth/google/login") + "?next=" + url.QueryEscape(target)
		redirectLocal(w, login, http.StatusFound)
	})
}
