// Package rabbitmq provides a publisher-only outbound adapter that emits
// fire-and-forget events to a NestJS Transport.RMQ consumer.
//
// Wire format (verified against the server consumer + the memo producer):
//
//   - The NestJS server consumes via @MessagePattern(..., Transport.RMQ) on a
//     named, durable queue (`collaboration-document-service`). NestJS's RMQ
//     ClientProxy.emit(pattern, data) publishes a JSON envelope
//     `{ "pattern": <string>, "data": <payload> }` onto that queue using the
//     DEFAULT exchange ("") with the routing key equal to the queue name.
//   - We reproduce that exact shape with raw amqp091: declare the queue durable
//     (idempotent with NestJS's own declaration) and publish the envelope to
//     exchange "" with routingKey = queue. Persistent delivery + JSON content
//     type, matching a NestJS-emitted message.
//
// This is intentionally NOT the Watermill topic-exchange model matrix-adapter
// uses for its golevelup consumers — a NestJS Transport.RMQ server reads from a
// queue, not a topic-exchange binding, so the topic-exchange route would never
// be delivered.
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

const publishTimeout = 5 * time.Second

// Publisher publishes NestJS-compatible event envelopes onto a single durable
// consumer queue. It lazily (re)connects and is safe for concurrent use.
type Publisher struct {
	url    string
	queue  string
	logger *zap.Logger

	mu      sync.Mutex
	conn    *amqp.Connection
	channel *amqp.Channel
	closed  bool
}

// NewPublisher creates a Publisher targeting the given NestJS consumer queue.
// It does not dial eagerly; the first Publish establishes the connection.
func NewPublisher(url, queue string, logger *zap.Logger) *Publisher {
	return &Publisher{url: url, queue: queue, logger: logger}
}

// envelope is the NestJS Transport.RMQ message shape produced by
// ClientProxy.emit(pattern, data).
type envelope struct {
	Pattern string `json:"pattern"`
	Data    any    `json:"data"`
}

// Publish emits {pattern: topic, data: payload} onto the consumer queue.
// Errors are returned (the caller logs and swallows them — best effort, FR-006).
func (p *Publisher) Publish(topic string, payload any) error {
	body, err := json.Marshal(envelope{Pattern: topic, Data: payload})
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return fmt.Errorf("publisher closed")
	}

	ch, err := p.ensureChannelLocked()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), publishTimeout)
	defer cancel()

	// Default exchange (""), routing key = queue name: this is how NestJS RMQ
	// delivers emitted events to a Transport.RMQ consumer.
	err = ch.PublishWithContext(ctx, "", p.queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
	if err != nil {
		// Drop the channel so the next call reconnects.
		p.resetLocked()
		return fmt.Errorf("publish to %q: %w", p.queue, err)
	}
	return nil
}

// ensureChannelLocked returns a live channel, dialing/declaring as needed.
// Caller must hold p.mu.
func (p *Publisher) ensureChannelLocked() (*amqp.Channel, error) {
	if p.conn != nil && !p.conn.IsClosed() && p.channel != nil && !p.channel.IsClosed() {
		return p.channel, nil
	}

	p.resetLocked()

	conn, err := amqp.Dial(p.url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	// Declare the consumer queue durable so publishing succeeds even if the
	// server hasn't connected yet. Durable matches NestJS queueOptions and is
	// idempotent with the consumer's own declaration.
	if _, err := ch.QueueDeclare(p.queue, true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare queue %q: %w", p.queue, err)
	}

	p.conn = conn
	p.channel = ch
	return ch, nil
}

// resetLocked tears down the current connection/channel. Caller must hold p.mu.
func (p *Publisher) resetLocked() {
	if p.channel != nil {
		_ = p.channel.Close()
		p.channel = nil
	}
	if p.conn != nil {
		_ = p.conn.Close()
		p.conn = nil
	}
}

// Close releases the broker connection.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.resetLocked()
	return nil
}
