package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
)

func RequireAPIUser(tokens *apitokens.Service, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, err := apitokens.ParseBearer(r.Header.Get("Authorization"))
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", nil)
				return
			}
			u, err := tokens.Resolve(r.Context(), raw)
			if errors.Is(err, apitokens.ErrInvalidToken) {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized", nil)
				return
			}
			if err != nil {
				log.ErrorContext(r.Context(), "resolve api token failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
				httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
		})
	}
}
