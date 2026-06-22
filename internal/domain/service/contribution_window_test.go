package service

import (
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// stubPublisher captures published events for assertions and can simulate
// publish failure.
type stubPublisher struct {
	mu      sync.Mutex
	events  []captured
	failed  int
	failErr error
}

type captured struct {
	topic   string
	payload contributionEvent
}

func (s *stubPublisher) Publish(topic string, payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		s.failed++
		return s.failErr
	}
	ev, _ := payload.(contributionEvent)
	s.events = append(s.events, captured{topic: topic, payload: ev})
	return nil
}

func (s *stubPublisher) Close() error { return nil }

func (s *stubPublisher) all() []captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]captured(nil), s.events...)
}

func ids(refs []userRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

func newTestWindow(pub *stubPublisher) *ContributionWindow {
	return NewContributionWindow(pub, time.Hour, zap.NewNop())
}

// T018a / FR-012: a window with activity but no genuine modification publishes
// exactly one VIEW event (same body, distinct routing key) — it is no longer
// dropped.
func TestContributionWindow_NotModified_EmitsViewEvent(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	// Users active, but no MarkModified (autosave/no-op only).
	w.AddActor("doc-1", "actor-write", true)
	w.AddActor("doc-1", "actor-read", false)

	w.flush()

	events := pub.all()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 view event for an active-but-unmodified window, got %d", len(events))
	}
	ev := events[0]
	if ev.topic != ViewTopic {
		t.Errorf("topic = %q, want %q (ViewTopic)", ev.topic, ViewTopic)
	}
	if ev.payload.DocumentID != "doc-1" {
		t.Errorf("documentId = %q, want doc-1", ev.payload.DocumentID)
	}
	if got, want := ids(ev.payload.WriteUsers), []string{"actor-write"}; !slices.Equal(got, want) {
		t.Errorf("writeUsers = %v, want %v", got, want)
	}
	if got, want := ids(ev.payload.ReadonlyUsers), []string{"actor-read"}; !slices.Equal(got, want) {
		t.Errorf("readonlyUsers = %v, want %v", got, want)
	}
}

// T018a / FR-012: a document with no actors at all emits neither event.
func TestContributionWindow_NoActors_NoEvent(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	// MarkModified without any AddActor — no actor was ever recorded.
	w.MarkModified("doc-1")

	w.flush()

	if got := len(pub.all()); got != 0 {
		t.Fatalf("expected no events for a doc with no actors, got %d", got)
	}
}

// T015: write + read split; modified doc emits one event with both arrays.
func TestContributionWindow_WriteReadSplit(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	w.AddActor("doc-1", "A", true)
	w.AddActor("doc-1", "B", true)
	w.AddActor("doc-1", "C", false)
	w.MarkModified("doc-1")

	w.flush()

	events := pub.all()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.topic != ContributionTopic {
		t.Errorf("topic = %q, want %q", ev.topic, ContributionTopic)
	}
	if ev.payload.DocumentID != "doc-1" {
		t.Errorf("documentId = %q, want doc-1", ev.payload.DocumentID)
	}
	if got, want := ids(ev.payload.WriteUsers), []string{"A", "B"}; !slices.Equal(got, want) {
		t.Errorf("writeUsers = %v, want %v", got, want)
	}
	if got, want := ids(ev.payload.ReadonlyUsers), []string{"C"}; !slices.Equal(got, want) {
		t.Errorf("readonlyUsers = %v, want %v", got, want)
	}
}

// T015 edge (spec Assumptions): a modified doc with read-only actors but no
// write actor still emits, with writeUsers empty. (Chosen rule: emit.)
func TestContributionWindow_ModifiedNoWriteActor_EmitsEmptyWrite(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	w.AddActor("doc-1", "C", false)
	w.MarkModified("doc-1")

	w.flush()

	events := pub.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if len(events[0].payload.WriteUsers) != 0 {
		t.Errorf("writeUsers should be empty, got %v", ids(events[0].payload.WriteUsers))
	}
	if got, want := ids(events[0].payload.ReadonlyUsers), []string{"C"}; !slices.Equal(got, want) {
		t.Errorf("readonlyUsers = %v, want %v", got, want)
	}
}

// T019 / SC-002: continuous editing — repeated MarkModified + the same write
// actor across many requests in one window → exactly one event, deduped.
func TestContributionWindow_ContinuousEditing_OneDedupedEvent(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	for i := 0; i < 50; i++ {
		w.AddActor("doc-1", "A", true)
		w.MarkModified("doc-1")
	}

	w.flush()

	events := pub.all()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event for continuous editing, got %d", len(events))
	}
	if got, want := ids(events[0].payload.WriteUsers), []string{"A"}; !slices.Equal(got, want) {
		t.Errorf("writeUsers = %v, want %v (deduped)", got, want)
	}
}

// A flush clears state: the first window emits a contribution event; the next
// window — same actor active but no genuine modification — starts fresh and so
// emits a VIEW event (not a contribution one), proving the modified flag and
// actor sets were cleared.
func TestContributionWindow_FlushClearsState(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	w.AddActor("doc-1", "A", true)
	w.MarkModified("doc-1")
	w.flush()

	// Next window: same actor active but no genuine modification.
	w.AddActor("doc-1", "A", true)
	w.flush()

	events := pub.all()
	if len(events) != 2 {
		t.Fatalf("expected 2 events total across both windows, got %d", len(events))
	}
	if events[0].topic != ContributionTopic {
		t.Errorf("first window topic = %q, want %q", events[0].topic, ContributionTopic)
	}
	if events[1].topic != ViewTopic {
		t.Errorf("second window topic = %q, want %q (state cleared → view, not contribution)", events[1].topic, ViewTopic)
	}
}

// Per-document independence (T018a): a modified doc emits a contribution event
// and an active-but-not-modified doc emits a view event — routed by topic.
func TestContributionWindow_ModifiedAndViewSplitByTopic(t *testing.T) {
	pub := &stubPublisher{}
	w := newTestWindow(pub)

	w.AddActor("doc-1", "A", true)
	w.MarkModified("doc-1")
	w.AddActor("doc-2", "B", true) // active but not modified → view event

	w.flush()

	byDoc := map[string]captured{}
	for _, ev := range pub.all() {
		byDoc[ev.payload.DocumentID] = ev
	}
	if len(byDoc) != 2 {
		t.Fatalf("expected events for 2 docs, got %d", len(byDoc))
	}
	if got := byDoc["doc-1"].topic; got != ContributionTopic {
		t.Errorf("doc-1 topic = %q, want %q (ContributionTopic)", got, ContributionTopic)
	}
	if got := byDoc["doc-2"].topic; got != ViewTopic {
		t.Errorf("doc-2 topic = %q, want %q (ViewTopic)", got, ViewTopic)
	}
}
