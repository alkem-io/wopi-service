package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

const (
	// ContributionTopic is the NestJS message pattern (routing key) the server
	// consumes via @MessagePattern(..., Transport.RMQ). It travels in the
	// envelope's "pattern" field. Owned by ADR 0001 / feature 003.
	ContributionTopic = "collaboration-collabora-document-contribution"

	// ContributionQueue is the NestJS consumer queue the contribution event is
	// delivered onto (shared with the memo INFO/SAVE/FETCH patterns). Matches
	// server MessagingQueue.COLLABORATION_DOCUMENT_SERVICE.
	ContributionQueue = "collaboration-document-service"
)

// userRef is one actor entry in the event ({ "id": "<actorId>" }).
type userRef struct {
	ID string `json:"id"`
}

// contributionEvent is the published message body (ADR 0001):
// { documentId, writeUsers:[{id}], readonlyUsers:[{id}] }.
type contributionEvent struct {
	DocumentID    string    `json:"documentId"`
	WriteUsers    []userRef `json:"writeUsers"`
	ReadonlyUsers []userRef `json:"readonlyUsers"`
}

// docWindow accumulates per-document state for the current window.
type docWindow struct {
	modified bool
	writeIDs map[string]struct{}
	readIDs  map[string]struct{}
}

func newDocWindow() *docWindow {
	return &docWindow{
		writeIDs: make(map[string]struct{}),
		readIDs:  make(map[string]struct{}),
	}
}

// ContributionWindow tracks, per document (keyed by file_id), whether it was
// genuinely modified in the current window and which write- and read-capable
// actors were active on it. On each tick it publishes one aggregate event per
// modified document, then clears.
//
// Emission runs entirely on the ticker goroutine — off the WOPI request/save
// path — so it is inherently best-effort: publish errors are logged, counted,
// and swallowed (FR-006).
type ContributionWindow struct {
	publisher port.QueuePublisher
	window    time.Duration
	logger    *zap.Logger

	mu   sync.Mutex
	docs map[string]*docWindow

	// Observability counters (FR-011).
	emitted     atomic.Int64 // records published per process lifetime
	publishFail atomic.Int64 // publish failures / dropped events
}

// NewContributionWindow creates a window with the given publisher and duration.
func NewContributionWindow(publisher port.QueuePublisher, window time.Duration, logger *zap.Logger) *ContributionWindow {
	return &ContributionWindow{
		publisher: publisher,
		window:    window,
		logger:    logger,
		docs:      make(map[string]*docWindow),
	}
}

func (c *ContributionWindow) entryLocked(fileID string) *docWindow {
	d, ok := c.docs[fileID]
	if !ok {
		d = newDocWindow()
		c.docs[fileID] = d
	}
	return d
}

// MarkModified flags a document as genuinely modified in the current window.
// Called from PutFile when X-COOL-WOPI-IsModifiedByUser is true (FR-001).
func (c *ContributionWindow) MarkModified(fileID string) {
	if fileID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entryLocked(fileID).modified = true
}

// AddActor records an actor as active on a document in the current window,
// routed by token permission: write-capable → writeIDs, else readIDs
// (FR-002/FR-003). Set semantics dedup repeated activity by the same actor.
func (c *ContributionWindow) AddActor(fileID, actorID string, isWrite bool) {
	if fileID == "" || actorID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.entryLocked(fileID)
	if isWrite {
		d.writeIDs[actorID] = struct{}{}
	} else {
		d.readIDs[actorID] = struct{}{}
	}
}

// Start runs the flush ticker until ctx is cancelled.
func (c *ContributionWindow) Start(ctx context.Context) {
	ticker := time.NewTicker(c.window)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.flush()
		}
	}
}

// Flush publishes one event per modified document and clears the window. It is
// invoked on each tick; exported so it can be triggered explicitly (e.g. in
// tests or on a manual drain).
func (c *ContributionWindow) Flush() {
	c.flush()
}

// flush publishes one event per modified document and clears the window.
func (c *ContributionWindow) flush() {
	c.mu.Lock()
	snapshot := c.docs
	c.docs = make(map[string]*docWindow)
	c.mu.Unlock()

	var emitted int
	for fileID, d := range snapshot {
		if !d.modified {
			// Activity without a genuine modification → no event (FR-001/SC-007).
			continue
		}
		event := contributionEvent{
			DocumentID:    fileID,
			WriteUsers:    toUserRefs(d.writeIDs),
			ReadonlyUsers: toUserRefs(d.readIDs),
		}
		if err := c.publisher.Publish(ContributionTopic, event); err != nil {
			c.publishFail.Add(1)
			c.logger.Warn("contribution publish failed (dropped, best-effort)",
				zap.String("documentId", fileID),
				zap.Int("writeUsers", len(event.WriteUsers)),
				zap.Int("readonlyUsers", len(event.ReadonlyUsers)),
				zap.Error(err),
			)
			continue
		}
		emitted++
		c.emitted.Add(1)
		c.logger.Info("contribution event published",
			zap.String("documentId", fileID),
			zap.Int("writeUsers", len(event.WriteUsers)),
			zap.Int("readonlyUsers", len(event.ReadonlyUsers)),
		)
	}
	if emitted > 0 {
		c.logger.Debug("contribution window flushed", zap.Int("recordsEmitted", emitted))
	}
}

// EmittedCount returns the number of contribution events published so far
// (observability, FR-011).
func (c *ContributionWindow) EmittedCount() int64 { return c.emitted.Load() }

// PublishFailureCount returns the number of dropped/failed publishes
// (observability, FR-011).
func (c *ContributionWindow) PublishFailureCount() int64 { return c.publishFail.Load() }

func toUserRefs(set map[string]struct{}) []userRef {
	refs := make([]userRef, 0, len(set))
	for id := range set {
		refs = append(refs, userRef{ID: id})
	}
	return refs
}
