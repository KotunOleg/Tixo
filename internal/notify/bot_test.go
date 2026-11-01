package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KotunOleg/Tixo/internal/db/dbtest"
	dbgen "github.com/KotunOleg/Tixo/internal/db/gen"
	"github.com/KotunOleg/Tixo/internal/web/i18n"
	"github.com/KotunOleg/Tixo/web"
)

var botNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

type sentMessage struct {
	chatID int64
	text   string
}

type fakeClient struct {
	sent []sentMessage
	err  error
}

func (f *fakeClient) SendMessage(_ context.Context, chatID int64, text string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMessage{chatID, text})
	return nil
}

func (f *fakeClient) GetUpdates(context.Context, int64, time.Duration) ([]Update, error) {
	return nil, nil
}

func (f *fakeClient) SetWebhook(context.Context, string, string) error { return nil }

func (f *fakeClient) last(t *testing.T) string {
	t.Helper()
	if len(f.sent) == 0 {
		t.Fatal("bot sent no reply")
	}
	return f.sent[len(f.sent)-1].text
}

type botFixture struct {
	bot    *Bot
	client *fakeClient
	q      *dbgen.Queries
	pool   *pgxpool.Pool
}

func newBotFixture(t *testing.T) *botFixture {
	t.Helper()
	pool := dbtest.Pool(t)
	tr, err := i18n.New(web.FS)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{}
	bot := NewBot(pool, client, tr, BotConfig{BaseURL: "https://tixo.example", Username: "tixo_bot"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bot.now = func() time.Time { return botNow }
	return &botFixture{bot: bot, client: client, q: dbgen.New(pool), pool: pool}
}

func (f *botFixture) user(t *testing.T, email string) int64 {
	t.Helper()
	u, err := f.q.CreateUser(context.Background(), dbgen.CreateUserParams{Email: email, Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func (f *botFixture) issueToken(t *testing.T, userID int64) string {
	t.Helper()
	link, _, err := f.bot.IssueLink(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("start")
}

func (f *botFixture) send(t *testing.T, chatID int64, lang, text string) string {
	t.Helper()
	err := f.bot.HandleUpdate(context.Background(), Update{UpdateID: 1, Message: &Message{
		Chat: Chat{ID: chatID, Type: privateChat}, From: &User{LanguageCode: lang}, Text: text,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f.client.last(t)
}

func (f *botFixture) chatOf(t *testing.T, userID int64) (int64, string) {
	t.Helper()
	l, err := f.q.GetTelegramLink(context.Background(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ""
	}
	if err != nil {
		t.Fatal(err)
	}
	if !l.ChatID.Valid {
		return 0, l.Language
	}
	return l.ChatID.Int64, l.Language
}

func TestIssueLinkPointsToTheBotWithAStartParameterTelegramAccepts(t *testing.T) {
	f := newBotFixture(t)
	alice := f.user(t, "alice@example.com")

	link, expires, err := f.bot.IssueLink(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://t.me/tixo_bot?start=") {
		t.Errorf("link = %q", link)
	}
	token := f.issueToken(t, alice)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(token) {
		t.Errorf("start parameter %q breaks Telegram's limit of 64 chars from [A-Za-z0-9_-]", token)
	}
	if !expires.Equal(botNow.Add(LinkTokenTTL)) {
		t.Errorf("expires = %v", expires)
	}
}

func TestStartWithTokenLinksTheChatOnceInTheSendersLanguage(t *testing.T) {
	f := newBotFixture(t)
	alice := f.user(t, "alice@example.com")
	token := f.issueToken(t, alice)

	reply := f.send(t, 42, "en-US", "/start "+token)
	if !strings.Contains(reply, "Telegram is connected") {
		t.Errorf("reply = %q", reply)
	}
	if chat, lang := f.chatOf(t, alice); chat != 42 || lang != "en" {
		t.Errorf("chat = %d, lang = %q, want 42 en", chat, lang)
	}

	replay := f.send(t, 99, "uk", "/start "+token)
	if !strings.Contains(replay, "застаріло") || !strings.Contains(replay, "https://tixo.example/profile") {
		t.Errorf("replayed token reply = %q", replay)
	}
	if chat, _ := f.chatOf(t, alice); chat != 42 {
		t.Errorf("replayed token moved the link to chat %d", chat)
	}
}

func TestStartWithBadTokenDoesNotLink(t *testing.T) {
	cases := []struct {
		name  string
		token func(f *botFixture, userID int64) string
		age   time.Duration
	}{
		{"unknown token", func(*botFixture, int64) string { return "not-a-real-token" }, 0},
		{"expired token", func(f *botFixture, userID int64) string { return f.issueToken(t, userID) }, LinkTokenTTL},
		{"token with path characters", func(*botFixture, int64) string { return "../../profile" }, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newBotFixture(t)
			alice := f.user(t, "alice@example.com")
			token := tc.token(f, alice)
			f.bot.now = func() time.Time { return botNow.Add(tc.age) }

			reply := f.send(t, 42, "en", "/start "+token)
			if !strings.Contains(reply, "expired") || !strings.Contains(reply, "https://tixo.example/en/profile") {
				t.Errorf("reply = %q", reply)
			}
			if chat, _ := f.chatOf(t, alice); chat != 0 {
				t.Errorf("bad token linked chat %d", chat)
			}
		})
	}
}

func TestChatLinkedAgainMovesToTheNewAccount(t *testing.T) {
	f := newBotFixture(t)
	alice := f.user(t, "alice@example.com")
	bob := f.user(t, "bob@example.com")
	f.send(t, 42, "uk", "/start "+f.issueToken(t, alice))

	reply := f.send(t, 42, "uk", "/start "+f.issueToken(t, bob))
	if !strings.Contains(reply, "підключено") {
		t.Errorf("reply = %q", reply)
	}
	if chat, _ := f.chatOf(t, alice); chat != 0 {
		t.Errorf("alice still holds chat %d", chat)
	}
	if chat, lang := f.chatOf(t, bob); chat != 42 || lang != "uk" {
		t.Errorf("bob chat = %d, lang = %q", chat, lang)
	}
}

func TestStopUnlinksTheChatAndIsSafeToRepeat(t *testing.T) {
	f := newBotFixture(t)
	alice := f.user(t, "alice@example.com")
	f.send(t, 42, "uk", "/start "+f.issueToken(t, alice))

	for range 2 {
		if reply := f.send(t, 42, "uk", "/stop"); !strings.Contains(reply, "вимкнено") {
			t.Errorf("reply = %q", reply)
		}
	}
	if chat, _ := f.chatOf(t, alice); chat != 0 {
		t.Errorf("chat %d still linked after /stop", chat)
	}
}

func TestOtherMessagesGetHelpWithTheProfileLink(t *testing.T) {
	cases := []struct {
		text, lang, want string
	}{
		{"/start", "uk", "https://tixo.example/profile"},
		{"/start@tixo_bot", "en", "https://tixo.example/en/profile"},
		{"hello", "de", "https://tixo.example/en/profile"},
		{"", "uk", "https://tixo.example/profile"},
	}
	for _, tc := range cases {
		t.Run(tc.text+"/"+tc.lang, func(t *testing.T) {
			f := newBotFixture(t)
			if reply := f.send(t, 7, tc.lang, tc.text); !strings.Contains(reply, tc.want) {
				t.Errorf("reply = %q, want link %s", reply, tc.want)
			}
		})
	}
}

func TestGroupChatsCannotBeLinked(t *testing.T) {
	f := newBotFixture(t)
	alice := f.user(t, "alice@example.com")
	err := f.bot.HandleUpdate(context.Background(), Update{UpdateID: 1, Message: &Message{
		Chat: Chat{ID: -100, Type: "group"}, Text: "/start " + f.issueToken(t, alice),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.client.sent) != 0 {
		t.Errorf("bot replied in a group: %+v", f.client.sent)
	}
	if chat, _ := f.chatOf(t, alice); chat != 0 {
		t.Errorf("group chat %d linked to a user", chat)
	}
}

func TestHandleUpdateIgnoresUpdatesWithoutAMessage(t *testing.T) {
	f := newBotFixture(t)
	if err := f.bot.HandleUpdate(context.Background(), Update{UpdateID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(f.client.sent) != 0 {
		t.Errorf("bot replied to an empty update: %+v", f.client.sent)
	}
}
