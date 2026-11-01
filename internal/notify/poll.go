package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const pollErrorBackoff = 2 * time.Second

type UpdateHandler interface {
	HandleUpdate(ctx context.Context, u Update) error
}

func Poll(ctx context.Context, client Client, h UpdateHandler, wait time.Duration, log *slog.Logger) error {
	var offset int64
	for {
		updates, err := client.GetUpdates(ctx, offset, wait)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if errors.Is(err, ErrConflict) {
			return fmt.Errorf("telegram-poll: the bot has a webhook set, remove it first with `tixo telegram-set-webhook --delete`: %w", err)
		}
		if err != nil {
			backoff := pollErrorBackoff
			if ra, ok := errors.AsType[*RetryAfterError](err); ok && ra.After > backoff {
				backoff = ra.After
			}
			log.WarnContext(ctx, "telegram-poll: getUpdates failed, retrying", "error", err, "backoff", backoff.String())
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}
		for _, u := range updates {
			if err := h.HandleUpdate(ctx, u); err != nil {
				log.ErrorContext(ctx, "telegram-poll: handle update", "update_id", u.UpdateID, "error", err)
			}
			offset = u.UpdateID + 1
		}
	}
}
