//go:build live

package notify

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveBotAPISendsAMessage(t *testing.T) {
	token, chat := os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_LIVE_CHAT_ID")
	if token == "" || chat == "" {
		t.Skip("set TELEGRAM_BOT_TOKEN and TELEGRAM_LIVE_CHAT_ID to message a real chat")
	}
	chatID, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := NewBotAPIClient(token).SendMessage(ctx, chatID, "Tixo live test "+time.Now().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}
