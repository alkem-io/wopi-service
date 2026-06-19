package port

// QueuePublisher is the outbound port for publishing fire-and-forget events to
// the message broker. Implementations are publisher-only and best-effort: a
// publish failure is returned to the caller (which logs and swallows it) and
// never propagates onto the WOPI request/save path (FR-006). When no broker is
// configured the adapter is a no-op (FR-009).
type QueuePublisher interface {
	// Publish marshals payload to JSON and publishes it to the given topic.
	Publish(topic string, payload any) error
	// Close releases broker resources. Safe to call on a no-op publisher.
	Close() error
}
