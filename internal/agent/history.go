package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	historyPrefix = "agent:chat:"
	historyTTL    = 24 * time.Hour
	maxExchanges  = 20
)

type History struct {
	rdb    *redis.Client
	prefix string
}

func NewHistory(rdb *redis.Client) *History {
	return &History{rdb: rdb, prefix: historyPrefix}
}

func (h *History) key(userID int64) string { return h.prefix + strconv.FormatInt(userID, 10) }

func (h *History) Load(ctx context.Context, userID int64) ([]Message, error) {
	raw, err := h.rdb.LRange(ctx, h.key(userID), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("load chat history: %w", err)
	}
	msgs := make([]Message, 0, len(raw))
	for _, r := range raw {
		var m Message
		if err := json.Unmarshal([]byte(r), &m); err != nil {
			return nil, fmt.Errorf("decode chat history: %w", err)
		}
		msgs = append(msgs, m)
	}
	return lastExchanges(msgs, maxExchanges), nil
}

func (h *History) Append(ctx context.Context, userID int64, msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	values := make([]any, 0, len(msgs))
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("encode chat history: %w", err)
		}
		values = append(values, b)
	}
	key := h.key(userID)
	_, err := h.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.RPush(ctx, key, values...)
		p.Expire(ctx, key, historyTTL)
		return nil
	})
	if err != nil {
		return fmt.Errorf("append chat history: %w", err)
	}
	return nil
}

func (h *History) Reset(ctx context.Context, userID int64) error {
	if err := h.rdb.Del(ctx, h.key(userID)).Err(); err != nil {
		return fmt.Errorf("reset chat history: %w", err)
	}
	return nil
}

func lastExchanges(msgs []Message, n int) []Message {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].isUserText() {
			continue
		}
		seen++
		if seen == n {
			return msgs[i:]
		}
	}
	for i, m := range msgs {
		if m.isUserText() {
			return msgs[i:]
		}
	}
	return nil
}
