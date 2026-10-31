package crm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	RoleBuyer     = "buyer"
	RoleOrganizer = "organizer"

	hubspotBaseURL = "https://api.hubapi.com"
	hubspotTimeout = 10 * time.Second
)

type Contact struct {
	Email string
	Name  string
	Role  string
}

type Client interface {
	UpsertContact(ctx context.Context, c Contact) error
}

type HubSpotClient struct {
	token   string
	baseURL string
	client  *http.Client
}

func NewHubSpotClient(token string) *HubSpotClient {
	return NewHubSpotClientWith(token, hubspotBaseURL, &http.Client{Timeout: hubspotTimeout})
}

func NewHubSpotClientWith(token, baseURL string, client *http.Client) *HubSpotClient {
	return &HubSpotClient{token: token, baseURL: baseURL, client: client}
}

type hubspotProperties struct {
	Email     string `json:"email"`
	FirstName string `json:"firstname"`
	TixoRole  string `json:"tixo_role"`
}

type hubspotUpsertBody struct {
	Properties hubspotProperties `json:"properties"`
}

func (h *HubSpotClient) UpsertContact(ctx context.Context, c Contact) error {
	body, err := json.Marshal(hubspotUpsertBody{Properties: hubspotProperties{
		Email: c.Email, FirstName: c.Name, TixoRole: c.Role,
	}})
	if err != nil {
		return fmt.Errorf("crm: marshal contact: %w", err)
	}

	endpoint := fmt.Sprintf("%s/crm/v3/objects/contacts/%s?idProperty=email", h.baseURL, url.PathEscape(c.Email))
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("crm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.token)

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("crm: upsert contact: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("crm: upsert contact: status %d", resp.StatusCode)
	}
	return nil
}

type DryRunClient struct {
	Log *slog.Logger
}

func (d *DryRunClient) UpsertContact(_ context.Context, c Contact) error {
	d.Log.Info("crm: dry-run upsert contact", "email", c.Email, "role", c.Role)
	return nil
}
