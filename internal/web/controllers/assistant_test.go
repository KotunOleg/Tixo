package controllers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/KotunOleg/Tixo/internal/agent"
	"github.com/KotunOleg/Tixo/internal/mcpserver"
	"github.com/KotunOleg/Tixo/internal/tools"
)

const assistantRate = 3

type assistantSite struct {
	*authSite
	rdb    *redis.Client
	cookie *http.Cookie
	userID int64
}

func newAssistantSite(t *testing.T, model agent.Model) *assistantSite {
	t.Helper()
	return newAssistantSiteWithTurnLimit(t, model, 0)
}

func newAssistantSiteWithTurnLimit(t *testing.T, model agent.Model, turnLimit time.Duration) *assistantSite {
	t.Helper()
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opts)
	s := newAuthSiteTweaked(t, nil, nil, func(d *Deps) {
		ts := tools.New(d.Events, d.Orders, testBaseURL, time.Now)
		d.Assistant = agent.New(agent.Deps{
			Model: model, Tools: ts, MCP: mcpserver.New(ts, d.Logger, "test"), History: agent.NewHistory(rdb), Log: d.Logger, Now: time.Now,
		})
		d.AssistantLimits = agent.NewLimits(rdb, assistantRate)
		d.AssistantTurnLimit = turnLimit
	})
	site := &assistantSite{authSite: s, rdb: rdb}
	site.cookie = s.signIn(t)
	site.userID = s.sessionUserID(t, site.cookie)
	site.clean()
	t.Cleanup(func() {
		site.clean()
		_ = rdb.Close()
	})
	return site
}

func (s *assistantSite) clean() {
	ctx := context.Background()
	uid := strconv.FormatInt(s.userID, 10)
	keys, _ := s.rdb.Keys(ctx, "agent:*:"+uid+"*").Result()
	keys = append(keys, "agent:chat:"+uid, "agent:busy:"+uid)
	_ = s.rdb.Del(ctx, keys...).Err()
}

func (s *assistantSite) chat(t *testing.T, ctx context.Context, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/en/assistant/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

type sseEvent struct {
	name string
	data string
}

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if cur.name == "" || !json.Valid([]byte(cur.data)) {
				t.Fatalf("malformed SSE frame %+v in %q", cur, body)
			}
			out = append(out, cur)
			cur = sseEvent{}
		}
	}
	return out
}

func TestAssistantChatStreamsEventsInOrder(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	rec := s.chat(t, t.Context(), `{"message":"what is on?"}`, s.cookie)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" || rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("status %d, headers %v, want an unbuffered event stream", rec.Code, rec.Header())
	}
	var names []string
	for _, e := range parseSSE(t, rec.Body.String()) {
		if len(names) == 0 || names[len(names)-1] != e.name {
			names = append(names, e.name)
		}
	}
	got := strings.Join(names, ",")
	if !strings.HasPrefix(got, agent.EventThinking+","+agent.EventToolCall+","+agent.EventToolResult) || !strings.HasSuffix(got, agent.EventDone) {
		t.Fatalf("events = %s, want thinking, tool_call, tool_result first and done last", got)
	}
}

func TestAssistantChatRejectsBadRequests(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	for _, tc := range []struct {
		name   string
		body   string
		cookie *http.Cookie
		want   int
	}{
		{"guest", `{"message":"hi"}`, nil, http.StatusUnauthorized},
		{"not json", `message=hi`, s.cookie, http.StatusBadRequest},
		{"empty message", `{"message":"  "}`, s.cookie, http.StatusBadRequest},
		{"too long", `{"message":"` + strings.Repeat("a", agent.MaxMessageLen+1) + `"}`, s.cookie, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := s.chat(t, t.Context(), tc.body, tc.cookie); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
	if keys, _ := s.rdb.Keys(context.Background(), "agent:rate:"+strconv.FormatInt(s.userID, 10)+":*").Result(); len(keys) != 0 {
		t.Error("rejected requests counted against the hourly limit")
	}
}

func TestAssistantChatLimitsMessagesPerHour(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	for i := range assistantRate {
		if rec := s.chat(t, t.Context(), `{"message":"rock"}`, s.cookie); rec.Code != http.StatusOK {
			t.Fatalf("message %d: status %d", i+1, rec.Code)
		}
	}
	rec := s.chat(t, t.Context(), `{"message":"rock"}`, s.cookie)
	var body struct {
		Limit   int       `json:"limit"`
		RetryAt time.Time `json:"retry_at"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusTooManyRequests || body.Limit != assistantRate || !body.RetryAt.After(time.Now()) || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d body %s, want 429 with the limit and when to retry", rec.Code, rec.Body.String())
	}
}

func TestAssistantChatAllowsOneStreamPerUser(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	if err := s.rdb.Set(context.Background(), "agent:busy:"+strconv.FormatInt(s.userID, 10), "other-tab", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if rec := s.chat(t, t.Context(), `{"message":"rock"}`, s.cookie); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while another reply streams", rec.Code)
	}
}

type hangingModel struct {
	cancel   context.CancelFunc
	canceled chan bool
}

func (m hangingModel) Next(ctx context.Context, _ agent.Request, sink agent.Sink) (agent.Reply, error) {
	_ = sink.Text("partial")
	m.cancel()
	select {
	case <-ctx.Done():
		m.canceled <- true
		return agent.Reply{}, ctx.Err()
	case <-time.After(5 * time.Second):
		m.canceled <- false
		return agent.Reply{Blocks: []agent.Block{{Type: agent.BlockText, Text: "late"}}, StopReason: agent.StopEndTurn}, nil
	}
}

func TestAssistantClientDisconnectCancelsTheModelAndFreesTheUser(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m := hangingModel{cancel: cancel, canceled: make(chan bool, 1)}
	s := newAssistantSite(t, m)
	s.chat(t, ctx, `{"message":"rock"}`, s.cookie)
	if !<-m.canceled {
		t.Fatal("model kept generating after the client went away")
	}
	busy, err := s.rdb.Exists(context.Background(), "agent:busy:"+strconv.FormatInt(s.userID, 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if busy != 0 {
		t.Fatal("busy lock left after the stream ended, the next message would get 409")
	}
	history, err := s.rdb.LLen(context.Background(), "agent:chat:"+strconv.FormatInt(s.userID, 10)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if history != 0 {
		t.Errorf("history has %d messages from an aborted turn", history)
	}
}

type stallingModel struct{}

func (stallingModel) Next(ctx context.Context, _ agent.Request, sink agent.Sink) (agent.Reply, error) {
	_ = sink.Text("still thinking")
	<-ctx.Done()
	return agent.Reply{}, ctx.Err()
}

func TestAssistantChatEmitsAnErrorWhenTheTurnDeadlineIsReached(t *testing.T) {
	s := newAssistantSiteWithTurnLimit(t, stallingModel{}, 50*time.Millisecond)
	rec := s.chat(t, t.Context(), `{"message":"rock"}`, s.cookie)
	events := parseSSE(t, rec.Body.String())
	if len(events) == 0 {
		t.Fatal("no SSE events received")
	}
	last := events[len(events)-1]
	var failure struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(last.data), &failure)
	if last.name != agent.EventError || failure.Code != agent.ErrUpstream {
		t.Fatalf("last event = %s %s, want an upstream error once the turn deadline is reached", last.name, last.data)
	}
}

func TestAssistantResetForgetsTheConversation(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	if rec := s.chat(t, t.Context(), `{"message":"rock"}`, s.cookie); rec.Code != http.StatusOK {
		t.Fatal(rec.Code)
	}
	key := "agent:chat:" + strconv.FormatInt(s.userID, 10)
	if n, _ := s.rdb.LLen(context.Background(), key).Result(); n == 0 {
		t.Fatal("chat was not saved")
	}
	if rec := s.request(t, http.MethodPost, "/assistant/reset", s.cookie); rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d", rec.Code)
	}
	if n, _ := s.rdb.LLen(context.Background(), key).Result(); n != 0 {
		t.Fatalf("history still has %d messages after reset", n)
	}
}

func TestAssistantPageShowsTheSavedConversationAsText(t *testing.T) {
	s := newAssistantSite(t, agent.Fake{})
	empty := s.request(t, http.MethodGet, "/en/assistant", s.cookie).Body.String()
	if !strings.Contains(empty, "js-assistant-suggest") || strings.Contains(empty, "tixo-msg-user") {
		t.Fatal("fresh page should offer suggestions and no messages")
	}
	if err := agent.NewHistory(s.rdb).Append(context.Background(), s.userID, []agent.Message{
		{Role: agent.RoleUser, Blocks: []agent.Block{{Type: agent.BlockText, Text: "<img src=x onerror=alert(1)>"}}},
		{Role: agent.RoleAssistant, Blocks: []agent.Block{{Type: agent.BlockThinking, Text: "secret plan"}, {Type: agent.BlockText, Text: "Nothing found"}}},
	}); err != nil {
		t.Fatal(err)
	}
	body := s.request(t, http.MethodGet, "/en/assistant", s.cookie).Body.String()
	if strings.Contains(body, "<img src=x") || !strings.Contains(body, "&lt;img src=x") {
		t.Error("saved message was not escaped")
	}
	if !strings.Contains(body, "Nothing found") || strings.Contains(body, "secret plan") {
		t.Error("page should show the answer text and leave thinking out")
	}
}
