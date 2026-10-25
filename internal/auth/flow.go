package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/url"
	"strings"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/oauth2"

	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
)

const (
	keyState    = "oauth.state"
	keyVerifier = "oauth.verifier"
	keyNext     = "oauth.next"
	keyUserID   = "user_id"
	keyFlash    = "flash"
	keyPurchase = "ga4.purchase"
)

var ErrBadState = errors.New("oauth state is missing or does not match")

func BeginLogin(ctx context.Context, sm *scs.SessionManager, next string) (state, verifier string) {
	state = rand.Text()
	verifier = oauth2.GenerateVerifier()
	sm.Put(ctx, keyState, state)
	sm.Put(ctx, keyVerifier, verifier)
	sm.Put(ctx, keyNext, SafeNext(next))
	return state, verifier
}

func FinishLogin(ctx context.Context, sm *scs.SessionManager, gotState string) (verifier, next string, err error) {
	want := sm.PopString(ctx, keyState)
	verifier = sm.PopString(ctx, keyVerifier)
	next = SafeNext(sm.PopString(ctx, keyNext))
	if want == "" || verifier == "" || subtle.ConstantTimeCompare([]byte(want), []byte(gotState)) != 1 {
		return "", next, ErrBadState
	}
	return verifier, next, nil
}

func SignIn(ctx context.Context, sm *scs.SessionManager, userID int64) error {
	if err := sm.RenewToken(ctx); err != nil {
		return err
	}
	sm.Put(ctx, keyUserID, userID)
	return nil
}

func SignOut(ctx context.Context, sm *scs.SessionManager) error {
	return sm.Destroy(ctx)
}

func UserID(ctx context.Context, sm *scs.SessionManager) (int64, bool) {
	id := sm.GetInt64(ctx, keyUserID)
	return id, id != 0
}

func ForgetUser(ctx context.Context, sm *scs.SessionManager) {
	sm.Remove(ctx, keyUserID)
}

type userKey struct{}

func WithUser(ctx context.Context, u dbgen.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func CurrentUser(ctx context.Context) (dbgen.User, bool) {
	u, ok := ctx.Value(userKey{}).(dbgen.User)
	return u, ok
}

func SetFlash(ctx context.Context, sm *scs.SessionManager, messageID string) {
	sm.Put(ctx, keyFlash, messageID)
}

func PopFlash(ctx context.Context, sm *scs.SessionManager) string {
	return sm.PopString(ctx, keyFlash)
}

func SetPurchase(ctx context.Context, sm *scs.SessionManager, payloadJSON string) {
	sm.Put(ctx, keyPurchase, payloadJSON)
}

func PopPurchase(ctx context.Context, sm *scs.SessionManager) (string, bool) {
	payload := sm.PopString(ctx, keyPurchase)
	return payload, payload != ""
}

func SafeNext(raw string) string {
	next := LocalPath(raw)
	if isAuthPath(next) {
		return "/"
	}
	return next
}

func LocalPath(raw string) string {
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "/"
	}
	return raw
}

func isAuthPath(p string) bool {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return strings.HasPrefix(p, "/auth/") || strings.HasPrefix(p, "/en/auth/")
}
