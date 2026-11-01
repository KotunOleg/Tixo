package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/auth"
	"github.com/KotunOleg/Tixo/internal/notify"
	"github.com/KotunOleg/Tixo/internal/platform/httpx"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/internal/web/view"
)

type apiTokenRow struct {
	ID         int64
	Name       string
	CreatedAt  string
	LastUsedAt string
}

type telegramSection struct {
	State        string
	Expires      string
	Linked       bool
	Pending      bool
	LinkedAt     string
	PendingUntil string
}

type profileData struct {
	Telegram   *telegramSection
	Tokens     []apiTokenRow
	Name       string
	Errors     map[string]string
	JustIssued bool
	IssuedName string
	IssuedRaw  string
}

type Profile struct {
	View     *view.Renderer
	Sessions *scs.SessionManager
	Svc      *apitokens.Service
	Telegram *notify.Bot
	Log      *slog.Logger
}

func (p Profile) Show(w http.ResponseWriter, r *http.Request) {
	p.render(w, r, http.StatusOK, profileData{})
}

func (p Profile) CreateToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		p.render(w, r, http.StatusBadRequest, profileData{})
		return
	}
	u, _ := auth.CurrentUser(r.Context())
	name := r.PostForm.Get("name")
	raw, token, err := p.Svc.Issue(r.Context(), u.ID, name)
	var verr *apitokens.ValidationError
	if errors.As(err, &verr) {
		errs := make(map[string]string, len(verr.Fields))
		for field, code := range verr.Fields {
			errs[field] = "profile.tokens.form.error." + field + "_" + code
		}
		p.render(w, r, http.StatusUnprocessableEntity, profileData{Name: name, Errors: errs})
		return
	}
	if err != nil {
		p.serverError(w, r, err)
		return
	}
	p.Log.InfoContext(r.Context(), "api token issued", "token_id", token.ID, "user_id", u.ID, "request_id", middleware.GetReqID(r.Context()))
	p.render(w, r, http.StatusCreated, profileData{JustIssued: true, IssuedName: token.Name, IssuedRaw: raw})
}

func (p Profile) RevokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		p.View.NotFound(w, r)
		return
	}
	u, _ := auth.CurrentUser(r.Context())
	if err := p.Svc.Revoke(r.Context(), u.ID, id); errors.Is(err, apitokens.ErrNotFound) {
		p.View.NotFound(w, r)
		return
	} else if err != nil {
		p.serverError(w, r, err)
		return
	}
	p.Log.InfoContext(r.Context(), "api token revoked", "token_id", id, "user_id", u.ID, "request_id", middleware.GetReqID(r.Context()))
	auth.SetFlash(r.Context(), p.Sessions, "profile.tokens.revoked")
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/profile"), http.StatusSeeOther)
}

func (p Profile) LinkTelegram(w http.ResponseWriter, r *http.Request) {
	if p.Telegram == nil {
		p.View.NotFound(w, r)
		return
	}
	u, _ := auth.CurrentUser(r.Context())
	link, _, err := p.Telegram.IssueLink(r.Context(), u.ID)
	if err != nil {
		p.serverError(w, r, err)
		return
	}
	p.Log.InfoContext(r.Context(), "telegram link issued", "user_id", u.ID, "request_id", middleware.GetReqID(r.Context()))
	redirect(w, link, http.StatusSeeOther)
}

func (p Profile) UnlinkTelegram(w http.ResponseWriter, r *http.Request) {
	if p.Telegram == nil {
		p.View.NotFound(w, r)
		return
	}
	u, _ := auth.CurrentUser(r.Context())
	if err := p.Telegram.Unlink(r.Context(), u.ID); err != nil {
		p.serverError(w, r, err)
		return
	}
	p.Log.InfoContext(r.Context(), "telegram unlinked", "user_id", u.ID, "request_id", middleware.GetReqID(r.Context()))
	auth.SetFlash(r.Context(), p.Sessions, "profile.telegram.unlinked")
	redirectLocal(w, i18n.Path(i18n.FromContext(r.Context()), "/profile"), http.StatusSeeOther)
}

func (p Profile) TelegramStatus(w http.ResponseWriter, r *http.Request) {
	if p.Telegram == nil {
		p.View.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	u, _ := auth.CurrentUser(r.Context())
	st, err := p.Telegram.Status(r.Context(), u.ID)
	if err != nil {
		p.Log.ErrorContext(r.Context(), "telegram status failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
		httpx.WriteError(w, http.StatusInternalServerError, "internal error", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, telegramStatusJSON{State: telegramState(st), Expires: telegramExpires(st)})
}

type telegramStatusJSON struct {
	State   string `json:"state"`
	Expires string `json:"expires,omitempty"`
}

func telegramState(st notify.LinkStatus) string {
	switch {
	case st.Linked():
		return "linked"
	case st.Pending():
		return "pending"
	default:
		return "none"
	}
}

func telegramExpires(st notify.LinkStatus) string {
	if !st.Pending() {
		return ""
	}
	return st.PendingUntil.UTC().Format(time.RFC3339Nano)
}

func (p Profile) telegramSection(r *http.Request, userID int64) (*telegramSection, error) {
	if p.Telegram == nil {
		return nil, nil
	}
	st, err := p.Telegram.Status(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	sec := &telegramSection{Linked: st.Linked(), Pending: st.Pending(), State: telegramState(st), Expires: telegramExpires(st)}
	if sec.Linked {
		sec.LinkedAt = st.LinkedAt.In(kyiv).Format(dateLayout)
	}
	if sec.Pending {
		sec.PendingUntil = st.PendingUntil.In(kyiv).Format(timeLayout)
	}
	return sec, nil
}

func (p Profile) render(w http.ResponseWriter, r *http.Request, status int, data profileData) {
	u, _ := auth.CurrentUser(r.Context())
	list, err := p.Svc.List(r.Context(), u.ID)
	if err != nil {
		p.serverError(w, r, err)
		return
	}
	rows := make([]apiTokenRow, len(list))
	for i, t := range list {
		var lastUsed string
		if t.LastUsedAt.Valid {
			lastUsed = t.LastUsedAt.Time.In(kyiv).Format(dateLayout)
		}
		rows[i] = apiTokenRow{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt.In(kyiv).Format(dateLayout), LastUsedAt: lastUsed}
	}
	data.Tokens = rows
	data.Telegram, err = p.telegramSection(r, u.ID)
	if err != nil {
		p.serverError(w, r, err)
		return
	}
	p.View.Render(w, r, status, "pages/profile", data)
}

func (p Profile) serverError(w http.ResponseWriter, r *http.Request, err error) {
	p.Log.ErrorContext(r.Context(), "profile handler failed", "error", err, "request_id", middleware.GetReqID(r.Context()))
	p.View.ServerError(w, r)
}
