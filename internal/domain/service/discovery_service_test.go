package service

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/alkem-io/wopi-service/internal/domain/port"
)

type mockDiscoveryClient struct {
	data *port.DiscoveryData
	err  error
}

func (m *mockDiscoveryClient) FetchDiscovery(_ context.Context) (*port.DiscoveryData, error) {
	return m.data, m.err
}

func TestGetDiscovery_FetchesOnFirstCall(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Word", Name: "edit", Ext: "docx", URLSrc: "https://collabora/edit"},
		},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())

	result, err := svc.GetDiscovery(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Errorf("expected 1 action, got %d", len(result.Actions))
	}
	if result.Actions[0].Ext != "docx" {
		t.Errorf("expected docx, got %s", result.Actions[0].Ext)
	}
}

func TestGetDiscovery_ReturnsCachedOnSecondCall(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{{Ext: "docx"}},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())

	// First call — primes cache
	_, _ = svc.GetDiscovery(context.Background())

	// Make client fail on subsequent calls
	client.err = errors.New("network error")
	client.data = nil

	// Second call should return cache
	result, err := svc.GetDiscovery(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Actions) != 1 {
		t.Errorf("expected cached result with 1 action, got %d", len(result.Actions))
	}
}

func TestGetDiscovery_FallbackToStaleCache(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{{Ext: "odt"}},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())

	// Prime cache
	_, _ = svc.GetDiscovery(context.Background())

	// Force cache expiry
	svc.mu.Lock()
	svc.cachedAt = svc.cachedAt.Add(-svc.cacheTTL * 2)
	svc.mu.Unlock()

	// Make client fail
	client.err = errors.New("collabora down")
	client.data = nil

	// Should return stale cache
	result, err := svc.GetDiscovery(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Actions[0].Ext != "odt" {
		t.Errorf("expected stale cache with odt, got %s", result.Actions[0].Ext)
	}
}

func TestGetDiscovery_ErrorWhenNoCache(t *testing.T) {
	client := &mockDiscoveryClient{err: errors.New("collabora down")}
	svc := NewDiscoveryService(client, zap.NewNop())

	_, err := svc.GetDiscovery(context.Background())
	if err == nil {
		t.Fatal("expected error when no cache and fetch fails")
	}
}

func TestInvalidateAndRefresh(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{{Ext: "xlsx"}},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())

	// Prime cache
	_, _ = svc.GetDiscovery(context.Background())

	// Update the data
	newData := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{{Ext: "pptx"}},
	}
	client.data = newData

	// Invalidate and refresh
	result, err := svc.InvalidateAndRefresh(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Actions[0].Ext != "pptx" {
		t.Errorf("expected pptx after refresh, got %s", result.Actions[0].Ext)
	}
}

// --- FindActionByExtension tests ---

func TestFindActionByExtension_EditPreferred(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Writer", Name: "view", Ext: "docx", URLSrc: "http://collabora/view"},
			{App: "Writer", Name: "edit", Ext: "docx", URLSrc: "http://collabora/edit"},
		},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background())

	action, err := svc.FindActionByExtension("docx", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action.Name != "edit" {
		t.Errorf("expected edit action, got %s", action.Name)
	}
}

func TestFindActionByExtension_ViewPreferred(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Writer", Name: "edit", Ext: "docx", URLSrc: "http://collabora/edit"},
			{App: "Writer", Name: "view", Ext: "docx", URLSrc: "http://collabora/view"},
		},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background())

	action, err := svc.FindActionByExtension("docx", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action.Name != "view" {
		t.Errorf("expected view action, got %s", action.Name)
	}
}

func TestFindActionByExtension_FallbackToView(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Writer", Name: "view", Ext: "pdf", URLSrc: "http://collabora/view"},
		},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background())

	action, err := svc.FindActionByExtension("pdf", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action.Name != "view" {
		t.Errorf("expected view fallback, got %s", action.Name)
	}
}

func TestFindActionByExtension_UnknownExtension(t *testing.T) {
	data := &port.DiscoveryData{
		Actions: []port.DiscoveryAction{
			{App: "Writer", Name: "edit", Ext: "docx", URLSrc: "http://collabora/edit"},
		},
	}
	client := &mockDiscoveryClient{data: data}
	svc := NewDiscoveryService(client, zap.NewNop())
	_, _ = svc.GetDiscovery(context.Background())

	_, err := svc.FindActionByExtension("xyz", true)
	if !errors.Is(err, ErrUnsupportedExtension) {
		t.Errorf("expected ErrUnsupportedExtension, got %v", err)
	}
}

func TestFindActionByExtension_NoCache(t *testing.T) {
	svc := NewDiscoveryService(&mockDiscoveryClient{err: errors.New("down")}, zap.NewNop())

	_, err := svc.FindActionByExtension("docx", true)
	if !errors.Is(err, ErrNoDiscoveryData) {
		t.Errorf("expected ErrNoDiscoveryData, got %v", err)
	}
}
