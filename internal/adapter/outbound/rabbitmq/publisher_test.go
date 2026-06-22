package rabbitmq

import (
	"encoding/json"
	"testing"
)

// The NestJS Transport.RMQ server deserializes an emitted event as
// { "pattern": <string>, "data": <payload> }. This test pins that envelope
// shape so a regression can't silently break the consumer.
func TestEnvelope_MatchesNestJSEmitShape(t *testing.T) {
	type body struct {
		DocumentID    string   `json:"documentId"`
		WriteUsers    []string `json:"writeUsers"`
		ReadonlyUsers []string `json:"readonlyUsers"`
	}
	payload := body{
		DocumentID:    "doc-123",
		WriteUsers:    []string{"A"},
		ReadonlyUsers: []string{"B"},
	}

	raw, err := json.Marshal(envelope{Pattern: "collaboration-office-document-contribution", Data: payload})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded struct {
		Pattern string          `json:"pattern"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if decoded.Pattern != "collaboration-office-document-contribution" {
		t.Errorf("pattern = %q", decoded.Pattern)
	}

	var gotData body
	if err := json.Unmarshal(decoded.Data, &gotData); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if gotData.DocumentID != "doc-123" {
		t.Errorf("documentId = %q, want doc-123", gotData.DocumentID)
	}
	if len(gotData.WriteUsers) != 1 || gotData.WriteUsers[0] != "A" {
		t.Errorf("writeUsers = %v", gotData.WriteUsers)
	}
	if len(gotData.ReadonlyUsers) != 1 || gotData.ReadonlyUsers[0] != "B" {
		t.Errorf("readonlyUsers = %v", gotData.ReadonlyUsers)
	}
}

// FR-009: the no-op publisher (used when no broker is configured) never errors
// and discards everything.
func TestNoopPublisher_IsSilentNoOp(t *testing.T) {
	p := NewNoopPublisher()
	if err := p.Publish("any-topic", map[string]string{"k": "v"}); err != nil {
		t.Errorf("no-op Publish returned error: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Errorf("no-op Close returned error: %v", err)
	}
}

// A closed publisher rejects further publishes rather than panicking.
func TestPublisher_AfterClose_Errors(t *testing.T) {
	p := NewPublisher("amqp://localhost:5672/", "q", nil)
	_ = p.Close()
	if err := p.Publish("t", map[string]string{}); err == nil {
		t.Error("expected error publishing after Close")
	}
}
