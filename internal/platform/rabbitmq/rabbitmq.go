package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/health"
)

const Exchange = "tixo"

var (
	errUnreachable  = errors.New("rabbitmq: broker unreachable")
	errNotConfirmed = errors.New("rabbitmq: publish not confirmed")

	retryTTL = (10 * time.Second).Milliseconds()
)

func Dial(ctx context.Context, url string) (*amqp.Connection, error) {
	return amqp.DialConfig(url, amqp.Config{
		Dial: func(network, addr string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if deadline, ok := ctx.Deadline(); ok {
				if err := conn.SetDeadline(deadline); err != nil {
					_ = conn.Close()
					return nil, err
				}
			}
			return conn, nil
		},
	})
}

func Checker(url string) health.Checker {
	return health.CheckFunc{
		CheckerName: "rabbitmq",
		Fn: func(ctx context.Context) error {
			conn, err := Dial(ctx, url)
			if err != nil {
				return fmt.Errorf("%w: %s", errUnreachable, redact(err, url))
			}
			return conn.Close()
		},
	}
}

func redact(err error, url string) string {
	msg := err.Error()
	if u, perr := amqp.ParseURI(url); perr == nil && u.Password != "" {
		msg = strings.ReplaceAll(msg, u.Password, "<redacted>")
	}
	return msg
}

func DeclareTopology(ch *amqp.Channel, queue string) error {
	if err := ch.ExchangeDeclare(Exchange, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", Exchange, err)
	}
	retry, parking := queue+".retry", queue+".parking"

	if err := declareBoundQueue(ch, queue, amqp.Table{
		"x-dead-letter-exchange":    Exchange,
		"x-dead-letter-routing-key": retry,
	}); err != nil {
		return err
	}
	if err := declareBoundQueue(ch, retry, amqp.Table{
		"x-message-ttl":             retryTTL,
		"x-dead-letter-exchange":    Exchange,
		"x-dead-letter-routing-key": queue,
	}); err != nil {
		return err
	}
	if err := declareBoundQueue(ch, parking, nil); err != nil {
		return err
	}
	return nil
}

func declareBoundQueue(ch *amqp.Channel, name string, args amqp.Table) error {
	if _, err := ch.QueueDeclare(name, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	if err := ch.QueueBind(name, name, Exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue %s: %w", name, err)
	}
	return nil
}

func OpenConfirmChannel(conn *amqp.Connection) (*amqp.Channel, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("open channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}
	return ch, nil
}

func Publish(ctx context.Context, ch *amqp.Channel, exchange, routingKey string, body []byte) error {
	confirm, err := ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
	})
	if err != nil {
		return fmt.Errorf("publish to %s/%s: %w", exchange, routingKey, err)
	}
	ok, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for confirm on %s/%s: %w", exchange, routingKey, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s/%s", errNotConfirmed, exchange, routingKey)
	}
	return nil
}
