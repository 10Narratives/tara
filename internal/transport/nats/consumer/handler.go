package natscons

import (
	"context"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"
)

func NewMessageHandler(log *zap.Logger) Handler {
	return func(ctx context.Context, msg jetstream.Msg) error {
		log.Info("message received",
			zap.String("subject", msg.Subject()),
			zap.String("data", string(msg.Data())),
		)
		time.Sleep(time.Second)
		return nil
	}
}
