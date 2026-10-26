package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"github.com/KotunOleg/Tixo/internal/users"
)

const (
	CallbackPath       = "/auth/google/callback"
	googleUserinfoURL  = "https://openidconnect.googleapis.com/v1/userinfo"
	googleTimeout      = 10 * time.Second
	maxUserinfoBytes   = 1 << 20
	selectAccountParam = "select_account"
)

type GoogleEndpoints struct {
	Auth     oauth2.Endpoint
	Userinfo string
}

type Google struct {
	oauth    oauth2.Config
	userinfo string
	client   *http.Client
}

func NewGoogle(clientID, clientSecret string, baseURL url.URL) *Google {
	return NewGoogleWith(clientID, clientSecret, baseURL, GoogleEndpoints{Auth: endpoints.Google, Userinfo: googleUserinfoURL},
		&http.Client{Timeout: googleTimeout})
}

func NewGoogleWith(clientID, clientSecret string, baseURL url.URL, ep GoogleEndpoints, client *http.Client) *Google {
	redirect := baseURL
	redirect.Path = CallbackPath
	return &Google{
		oauth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     ep.Auth,
			RedirectURL:  redirect.String(),
			Scopes:       []string{"openid", "email", "profile"},
		},
		userinfo: ep.Userinfo,
		client:   client,
	}
}

func (g *Google) AuthCodeURL(state, verifier string) string {
	return g.oauth.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("prompt", selectAccountParam),
	)
}

type googleUserinfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func (g *Google) Profile(ctx context.Context, code, verifier string) (users.GoogleProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	tok, err := g.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return users.GoogleProfile{}, fmt.Errorf("google token exchange: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.userinfo, nil)
	if err != nil {
		return users.GoogleProfile{}, err
	}
	tok.SetAuthHeader(req)
	resp, err := g.client.Do(req)
	if err != nil {
		return users.GoogleProfile{}, fmt.Errorf("google userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return users.GoogleProfile{}, fmt.Errorf("google userinfo: status %d", resp.StatusCode)
	}
	var info googleUserinfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUserinfoBytes)).Decode(&info); err != nil {
		return users.GoogleProfile{}, fmt.Errorf("google userinfo: decode: %w", err)
	}
	return users.GoogleProfile{Sub: info.Sub, Email: info.Email, EmailVerified: info.EmailVerified, Name: info.Name}, nil
}
