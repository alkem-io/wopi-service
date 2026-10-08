package service

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

func newTestAdmission(waitingCapacity int) *PreviewService {
	return &PreviewService{
		slots:  semaphore.NewWeighted(int64(maxActiveRenders + waitingCapacity)),
		active: semaphore.NewWeighted(maxActiveRenders),
	}
}

func TestRenderAdmission_ZeroCapacityAllowsOnlyTwoActive(t *testing.T) {
	s := newTestAdmission(0)

	if !s.slots.TryAcquire(1) {
		t.Fatal("expected 1st admission to succeed")
	}
	if !s.slots.TryAcquire(1) {
		t.Fatal("expected 2nd admission to succeed")
	}
	if s.slots.TryAcquire(1) {
		t.Fatal("expected 3rd admission to fail with zero waiting capacity")
	}

	s.slots.Release(1)
	if !s.slots.TryAcquire(1) {
		t.Fatal("expected admission to succeed again after a release")
	}
}

func TestRenderAdmission_WaitingCapacityBoundsTotalAdmitted(t *testing.T) {
	s := newTestAdmission(8)

	admitted := 0
	for i := 0; i < 10; i++ { // 2 active + 8 waiting
		if s.slots.TryAcquire(1) {
			admitted++
		}
	}
	if admitted != 10 {
		t.Fatalf("admitted = %d, want 10 (2 active + 8 waiting)", admitted)
	}
	if s.slots.TryAcquire(1) {
		t.Fatal("11th admission must fail")
	}
}

func TestRenderAdmission_ActivateBoundsConcurrencyToTwo(t *testing.T) {
	s := newTestAdmission(8)

	if err := s.active.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("1st activate: %v", err)
	}
	if err := s.active.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("2nd activate: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.active.Acquire(ctx, 1); err == nil {
		t.Fatal("3rd activate must block until ctx deadline with two already active")
	}

	s.active.Release(1)
	if err := s.active.Acquire(context.Background(), 1); err != nil {
		t.Fatalf("activate after deactivate: %v", err)
	}
}
