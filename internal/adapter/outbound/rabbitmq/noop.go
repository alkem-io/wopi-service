package rabbitmq

// NoopPublisher is used when no broker is configured (FR-009). Every Publish is
// a silent success so the contribution window can run without a broker and
// without affecting the WOPI request path.
type NoopPublisher struct{}

// NewNoopPublisher returns a publisher that discards all events.
func NewNoopPublisher() *NoopPublisher { return &NoopPublisher{} }

// Publish discards the event.
func (NoopPublisher) Publish(_ string, _ any) error { return nil }

// Close is a no-op.
func (NoopPublisher) Close() error { return nil }
