package auth

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/alexedwards/scs/goredisstore"
	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
)

const (
	SessionCookieName = "tixo_session"
	sessionKeyPrefix  = "tixo:session:"
	sessionLifetime   = 30 * 24 * time.Hour
	sessionIdle       = 7 * 24 * time.Hour
)

func NewSessions(rdb *redis.Client, baseURL url.URL, log *slog.Logger, onError http.HandlerFunc) *scs.SessionManager {
	return NewSessionsWithStore(goredisstore.NewWithPrefix(rdb, sessionKeyPrefix), baseURL, log, onError)
}

func NewSessionsWithStore(store scs.Store, baseURL url.URL, log *slog.Logger, onError http.HandlerFunc) *scs.SessionManager {
	sm := scs.New()
	sm.Store = store
	sm.Lifetime = sessionLifetime
	sm.IdleTimeout = sessionIdle
	sm.Cookie.Name = SessionCookieName
	sm.Cookie.Path = "/"
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = baseURL.Scheme == "https"
	sm.Cookie.Persist = true
	sm.ErrorFunc = func(w http.ResponseWriter, r *http.Request, err error) {
		log.ErrorContext(r.Context(), "session store failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		if onError != nil {
			onError(w, r)
			return
		}
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
	return sm
}
