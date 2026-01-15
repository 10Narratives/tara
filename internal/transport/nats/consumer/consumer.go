package natscons

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type Handler func(ctx context.Context, msg jetstream.Msg) error

type Consumer struct {
	cons    jetstream.Consumer
	handler Handler

	maxWait  time.Duration
	maxBatch int

	slotsN int
	slots  chan struct{}

	mu     sync.Mutex
	runCtx context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewConsumer(stream jetstream.Stream, handler Handler, slots int) (*Consumer, error) {
	if handler == nil {
		return nil, errors.New("handler is nil")
	}
	if slots <= 0 {
		return nil, errors.New("slots must be > 0")
	}

	cons, err := stream.CreateConsumer(context.Background(), jetstream.ConsumerConfig{
		Durable:       "faas-agent",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxAckPending: slots,
	})
	if err != nil {
		return nil, err
	}

	c := &Consumer{
		cons:     cons,
		handler:  handler,
		maxWait:  2 * time.Second,
		maxBatch: 1,

		slotsN: slots,
		slots:  make(chan struct{}, slots),
	}
	for i := 0; i < slots; i++ {
		c.slots <- struct{}{}
	}

	return c, nil
}

func (c *Consumer) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return errors.New("consumer already running")
	}
	c.runCtx, c.cancel = context.WithCancel(ctx)
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		c.loop()
	}()

	return nil
}

func (c *Consumer) Stop(ctx context.Context) error {
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (c *Consumer) loop() {
	for {
		select {
		case <-c.runCtx.Done():
			return
		case <-c.slots:
		}

		batch, err := c.cons.Fetch(c.maxBatch, jetstream.FetchMaxWait(c.maxWait))
		if err != nil {
			c.slots <- struct{}{}
			continue
		}

		got := false
		for msg := range batch.Messages() {
			got = true

			c.wg.Add(1)
			go func(m jetstream.Msg) {
				defer c.wg.Done()
				defer func() { c.slots <- struct{}{} }()

				if err := c.handler(c.runCtx, m); err != nil {
					_ = m.Nak()
					return
				}
				_ = m.Ack()
			}(msg)
		}

		if !got {
			c.slots <- struct{}{}
		}
	}
}
