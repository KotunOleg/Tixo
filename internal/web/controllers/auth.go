package controllers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/users"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

const (
	msgAuthState      = "auth.error.state"
	msgAuthProvider   = "auth.error.provider"
	msgAuthUnverified = "auth.error.unverified"
	msgAuthTaken      = "auth.error.taken"
	msgAuthCancelled  = "auth.flash.cancelled"
)

type authErrorData struct {
	MessageID string
}

type Auth struct {
	View     *view.Renderer
	Sessions *scs.SessionManager
	Google   *auth.Google
	Users    *users.Service
	Log      *slog.Logger
}

func (a Auth) Login(w http.ResponseWriter, r *http.Request) {
	if a.Google == nil {
		a.View.NotFound(w, r)
		return
	}
	lang := i18n.FromContext(r.Context())
	next := i18n.Path(lang, auth.SafeNext(r.URL.Query().Get("next")))
	state, verifier := auth.BeginLogin(r.Context(), a.Sessions, next)
	redirect(w, a.Google.AuthCodeURL(state, verifier), http.StatusFound)
}

func (a Auth) Callback(w http.ResponseWriter, r *http.Request) {
	if a.Google == nil {
		a.View.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	verifier, next, err := auth.FinishLogin(r.Context(), a.Sessions, q.Get("state"))
	r = r.WithContext(i18n.WithLang(r.Context(), i18n.FromPath(next)))
	ctx := r.Context()
	if err != nil {
		a.fail(w, r, http.StatusBadRequest, msgAuthState, err)
		return
	}
	if reason := q.Get("error"); reason != "" {
		a.Log.InfoContext(ctx, "google sign-in cancelled", "reason", reason, "request_id", middleware.GetReqID(ctx))
		auth.SetFlash(ctx, a.Sessions, msgAuthCancelled)
		redirectLocal(w, next, http.StatusFound)
		return
	}

	profile, err := a.Google.Profile(ctx, q.Get("code"), verifier)
	if err != nil {
		a.fail(w, r, http.StatusBadGateway, msgAuthProvider, err)
		return
	}
	u, err := a.Users.SignInWithGoogle(ctx, profile)
	switch {
	case errors.Is(err, users.ErrEmailNotVerified):
		a.fail(w, r, http.StatusForbidden, msgAuthUnverified, err)
		return
	case errors.Is(err, users.ErrEmailTaken):
		a.fail(w, r, http.StatusConflict, msgAuthTaken, err)
		return
	case errors.Is(err, users.ErrInvalidProfile):
		a.fail(w, r, http.StatusBadGateway, msgAuthProvider, err)
		return
	case err != nil:
		a.serverError(w, r, err)
		return
	}
	if err := auth.SignIn(ctx, a.Sessions, u.ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.Log.InfoContext(ctx, "user signed in", "user_id", u.ID, "request_id", middleware.GetReqID(ctx))
	redirectLocal(w, next, http.StatusFound)
}

func (a Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if err := auth.SignOut(r.Context(), a.Sessions); err != nil {
		a.serverError(w, r, err)
		return
	}
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/"), http.StatusSeeOther)
}

func (a Auth) fail(w http.ResponseWriter, r *http.Request, status int, messageID string, err error) {
	a.Log.WarnContext(r.Context(), "google sign-in failed", "status", status, "error", err, "request_id", middleware.GetReqID(r.Context()))
	a.View.Render(w, r, status, "pages/auth_error", authErrorData{MessageID: messageID})
}

func (a Auth) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.ErrorContext(r.Context(), "sign-in handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	a.View.ServerError(w, r)
}

func redirectLocal(w http.ResponseWriter, target string, status int) {
	redirect(w, auth.SafeNext(target), status)
}

func redirect(w http.ResponseWriter, location string, status int) {
	w.Header().Set("Location", location)
	w.WriteHeader(status)
}
