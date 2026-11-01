package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	limitsPrefix = "agent:"
	busyTTL      = 3 * time.Minute
	rateWindow   = time.Hour
)

var ErrBusy = errors.New("another assistant reply is still streaming")

type RateLimited struct {
	Limit   int
	RetryAt time.Time
}

func (e *RateLimited) Error() string {
	return fmt.Sprintf("assistant limit of %d messages per hour reached", e.Limit)
}

type Limits struct {
	rdb     *redis.Client
	perHour int
	prefix  string
}

func NewLimits(rdb *redis.Client, perHour int) *Limits {
	return &Limits{rdb: rdb, perHour: perHour, prefix: limitsPrefix}
}

func (l *Limits) PerHour() int { return l.perHour }

func (l *Limits) Acquire(ctx context.Context, userID int64, now time.Time) (func(), error) {
	uid := strconv.FormatInt(userID, 10)
	busyKey := l.prefix + "busy:" + uid
	token := strconv.FormatInt(now.UnixNano(), 10)
	ok, err := l.rdb.SetNX(ctx, busyKey, token, busyTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("assistant busy lock: %w", err)
	}
	if !ok {
		return nil, ErrBusy
	}
	release := func() { _ = releaseScript.Run(context.WithoutCancel(ctx), l.rdb, []string{busyKey}, token).Err() }

	window := now.Truncate(rateWindow)
	rateKey := l.prefix + "rate:" + uid + ":" + strconv.FormatInt(window.Unix(), 10)
	var count *redis.IntCmd
	_, err = l.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		count = p.Incr(ctx, rateKey)
		p.Expire(ctx, rateKey, rateWindow+time.Minute)
		return nil
	})
	if err != nil {
		release()
		return nil, fmt.Errorf("assistant rate limit: %w", err)
	}
	if count.Val() > int64(l.perHour) {
		release()
		return nil, &RateLimited{Limit: l.perHour, RetryAt: window.Add(rateWindow)}
	}
	return release, nil
}

var releaseScript = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`)
