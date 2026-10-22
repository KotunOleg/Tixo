package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/KotunOleg/Tixo/internal/health"
)

var errUnreachable = errors.New("rabbitmq: broker unreachable")

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
