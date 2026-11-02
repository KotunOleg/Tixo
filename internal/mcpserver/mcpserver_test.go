package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/KotunOleg/Tixo/internal/apitokens"
	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/events"
	"github.com/KotunOleg/Tixo/internal/orders"
	"github.com/KotunOleg/Tixo/internal/organizers"
	"github.com/KotunOleg/Tixo/internal/tiers"
	"github.com/KotunOleg/Tixo/internal/tools"
	"github.com/KotunOleg/Tixo/internal/venues"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type fixture struct {
	pool   *pgxpool.Pool
	tokens *apitokens.Service
	srv    *httptest.Server
	buyer  int64
	tier   dbgen.TicketTier
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	ctx := context.Background()
	q := dbgen.New(pool)
	owner, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: "owner@example.com", Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	buyer, err := q.CreateUser(ctx, dbgen.CreateUserParams{Email: "buyer@example.com", Name: "buyer"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := organizers.New(pool).Create(ctx, owner.ID, organizers.CreateInput{Name: "Kyiv Live", Slug: "kyiv-live"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := venues.New(pool).Create(ctx, org.ID, venues.Input{Name: "Atlas", Address: "1 Test St", City: "Kyiv", Lat: 50.45, Lng: 30.52, Capacity: 1000})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	ev, err := events.New(pool).Create(ctx, org.ID, events.Input{
		VenueID: v.ID, Slug: "rock-night", Title: "Rock Night", Genre: events.GenreRock, StartsAt: start, EndsAt: start.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	tier, err := tiers.New(pool).Create(ctx, org.ID, ev.ID, tiers.Input{Name: "Fan zone", Price: 80000, Quantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.New(pool).SetStatus(ctx, org.ID, ev.ID, events.StatusPublished); err != nil {
		t.Fatal(err)
	}

	base, _ := url.Parse("https://tixo.test")
	log := slog.New(slog.DiscardHandler)
	tokens := apitokens.New(pool)
	s := New(tools.New(events.New(pool), orders.New(pool), *base, time.Now), log, "test")
	srv := httptest.NewServer(s.Handler(tokens))
	t.Cleanup(srv.Close)
	return fixture{pool: pool, tokens: tokens, srv: srv, buyer: buyer.ID, tier: tier}
}

func (f fixture) token(t *testing.T) (string, int64) {
	t.Helper()
	raw, tok, err := f.tokens.Issue(context.Background(), f.buyer, "claude code")
	if err != nil {
		t.Fatal(err)
	}
	return raw, tok.ID
}

func (f fixture) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   f.srv.URL,
		HTTPClient: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if out != nil && !res.IsError {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if txt, ok := c.(*mcp.TextContent); ok {
			b.WriteString(txt.Text)
		}
	}
	return b.String()
}

func TestMCPRejectsRequestsWithoutAValidToken(t *testing.T) {
	f := newFixture(t)
	raw, id := f.token(t)
	if err := f.tokens.Revoke(context.Background(), f.buyer, id); err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, tc := range []struct {
		name, header string
	}{
		{"no header", ""},
		{"not bearer", "Basic dXNlcjpwYXNz"},
		{"unknown token", "Bearer tixo_not_a_real_token"},
		{"revoked token", "Bearer " + raw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, f.srv.URL, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized || strings.Contains(string(got), "search_events") {
				t.Fatalf("status = %d body = %s, want 401 without the tool list", resp.StatusCode, got)
			}
		})
	}
}

func TestMCPListsThreeReadToolsAndOneWriteTool(t *testing.T) {
	f := newFixture(t)
	raw, _ := f.token(t)
	cs := f.connect(t, raw)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var readOnly, writes []string
	for _, tool := range res.Tools {
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			readOnly = append(readOnly, tool.Name)
			continue
		}
		writes = append(writes, tool.Name)
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || !tool.Annotations.IdempotentHint {
			t.Errorf("%s annotations = %+v, want destructive and idempotent so MCP clients ask before calling it", tool.Name, tool.Annotations)
		}
	}
	slices.Sort(readOnly)
	if !slices.Equal(readOnly, []string{tools.NameGetEvent, tools.NameListVenues, tools.NameSearchEvents}) || !slices.Equal(writes, []string{tools.NameBookTicket}) {
		t.Fatalf("read tools = %v, write tools = %v, want 3 read and book_ticket", readOnly, writes)
	}
}

func TestMCPSearchThenBookCreatesOneOrderForTheTokenOwner(t *testing.T) {
	f := newFixture(t)
	raw, _ := f.token(t)
	cs := f.connect(t, raw)

	var found tools.SearchEventsOutput
	call(t, cs, tools.NameSearchEvents, map[string]any{"query": "rock", "city": "Kyiv"}, &found)
	if len(found.Events) != 1 || found.Events[0].Slug != "rock-night" {
		t.Fatalf("search = %+v, want rock-night", found.Events)
	}

	var ev tools.Event
	call(t, cs, tools.NameGetEvent, map[string]any{"slug": "rock-night"}, &ev)
	if len(ev.Tiers) != 1 || ev.Tiers[0].TierID != f.tier.ID || !ev.OnSale {
		t.Fatalf("event = %+v, want the Fan zone tier on sale", ev)
	}

	args := map[string]any{"event_slug": "rock-night", "tier_id": f.tier.ID, "quantity": 2, "idempotency_key": "claude-order-1"}
	var first, retry tools.Order
	call(t, cs, tools.NameBookTicket, args, &first)
	call(t, cs, tools.NameBookTicket, args, &retry)
	if first.OrderID == 0 || retry.OrderID != first.OrderID || first.TotalUAH != 1600 {
		t.Fatalf("orders = %+v then %+v, want one order of 1600 UAH replayed on retry", first, retry)
	}

	var owner int64
	var count int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*), max(user_id) FROM orders").Scan(&count, &owner); err != nil {
		t.Fatal(err)
	}
	if count != 1 || owner != f.buyer {
		t.Fatalf("orders = %d owned by %d, want 1 owned by the token's user %d", count, owner, f.buyer)
	}
}

func TestMCPToolErrorsAreToolResultsWithoutInternals(t *testing.T) {
	f := newFixture(t)
	raw, _ := f.token(t)
	cs := f.connect(t, raw)

	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"unknown event", tools.NameGetEvent, map[string]any{"slug": "nope"}, tools.CodeNotFound},
		{"bad date", tools.NameSearchEvents, map[string]any{"date_from": "soon"}, "date_from"},
		{"too many tickets", tools.NameBookTicket, map[string]any{"event_slug": "rock-night", "tier_id": f.tier.ID, "quantity": 11, "idempotency_key": "claude-order-2"}, "quantity"},
		{"sold out", tools.NameBookTicket, map[string]any{"event_slug": "rock-night", "tier_id": f.tier.ID, "quantity": 10, "idempotency_key": "claude-order-3"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "sold out" {
				if _, err := f.pool.Exec(context.Background(), "UPDATE ticket_tiers SET available = 1"); err != nil {
					t.Fatal(err)
				}
				tc.want = tools.CodeSoldOut
			}
			res := call(t, cs, tc.tool, tc.args, nil)
			text := resultText(res)
			if !res.IsError || !strings.Contains(text, tc.want) || strings.Contains(text, "SQLSTATE") {
				t.Fatalf("result = %v %q, want isError mentioning %q", res.IsError, text, tc.want)
			}
		})
	}
}
