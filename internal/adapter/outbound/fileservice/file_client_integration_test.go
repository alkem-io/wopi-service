package fileservice

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"testing"

	"github.com/alkem-io/wopi-service/internal/domain/model"
)

// These tests drive the REAL file-service router against a database carrying
// the server-owned migrations. They exist because the preview design rests on
// three claims about the OWNING service that no fake can honestly establish:
//
//  1. two private preview rows in one bucket may hold identical bytes — there
//     is no content-uniqueness constraint to collide with, so PUT needs no
//     `skipDedup` and no 409-to-POST fallback;
//  2. PUT replaces content while preserving the logical row — its id, bucket,
//     display name and (absent) authorization all survive, so the preview
//     fileID the cache maps to is genuinely stable;
//  3. HEAD is not routed on the content path, which is why an existence probe
//     built on it reported every file absent.
//
// A hand-written fake HTTP handler would happily "prove" whichever of these
// its author believed. Skipped unless pointed at a live file-service.
//
// Run:
//
//	WOPI_IT_FILE_SERVICE_URL=http://localhost:4003 \
//	WOPI_IT_STORAGE_BUCKET_ID=<an existing storage_bucket id> \
//	go test ./internal/adapter/outbound/fileservice/ -run Integration -v

func integrationTarget(t *testing.T) (*FileClient, string, string) {
	t.Helper()
	baseURL := os.Getenv("WOPI_IT_FILE_SERVICE_URL")
	bucketID := os.Getenv("WOPI_IT_STORAGE_BUCKET_ID")
	if baseURL == "" || bucketID == "" {
		t.Skip("set WOPI_IT_FILE_SERVICE_URL and WOPI_IT_STORAGE_BUCKET_ID to run the file-service integration tests")
	}
	return NewFileClient(baseURL), baseURL, bucketID
}

// pngBytes builds a real 1x1 PNG. file-service sniffs and processes image
// content, so these tests cannot use arbitrary bytes.
func pngBytes(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// createPreview creates a private preview file and schedules its removal, so a
// run leaves the target database exactly as it found it.
func createPreview(t *testing.T, c *FileClient, bucketID string, content []byte) string {
	t.Helper()
	id, err := c.CreatePreviewFile(context.Background(), bucketID, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("CreatePreviewFile: %v", err)
	}
	t.Cleanup(func() {
		if derr := c.DeletePreviewFile(context.Background(), id); derr != nil {
			t.Errorf("cleanup delete %s: %v", id, derr)
		}
	})
	return id
}

// TestIntegration_IdenticalContentYieldsTwoIndependentRows refutes the
// content-collision premise directly: the same bytes, created twice into one
// bucket, produce two distinct logical rows that agree on their content
// identity (externalID) and conflict on nothing.
func TestIntegration_IdenticalContentYieldsTwoIndependentRows(t *testing.T) {
	client, _, bucketID := integrationTarget(t)
	content := pngBytes(t, color.RGBA{R: 255, A: 255})

	first := createPreview(t, client, bucketID, content)
	second := createPreview(t, client, bucketID, content)

	if first == second {
		t.Fatalf("two skipDedup creates returned one id %q — previews would share a logical file and could not be refreshed independently", first)
	}

	ctx := context.Background()
	firstMeta, err := client.FindByID(ctx, first)
	if err != nil || firstMeta == nil {
		t.Fatalf("FindByID(first) = %v, %v", firstMeta, err)
	}
	secondMeta, err := client.FindByID(ctx, second)
	if err != nil || secondMeta == nil {
		t.Fatalf("FindByID(second) = %v, %v", secondMeta, err)
	}
	if firstMeta.ExternalID != secondMeta.ExternalID {
		t.Errorf("externalIDs differ (%q vs %q) — expected identical content to hash identically",
			firstMeta.ExternalID, secondMeta.ExternalID)
	}
	if firstMeta.StorageBucketID != secondMeta.StorageBucketID {
		t.Errorf("rows landed in different buckets (%q vs %q); the collision claim is only meaningful within one bucket",
			firstMeta.StorageBucketID, secondMeta.StorageBucketID)
	}
}

// assertRowIdentityPreserved checks everything a content replacement must
// leave untouched: only the content and its updatedDate may move.
func assertRowIdentityPreserved(t *testing.T, before, after *model.Document) {
	t.Helper()
	if after.ID != before.ID {
		t.Errorf("preview id changed %q -> %q; the cache mapping would be dangling", before.ID, after.ID)
	}
	if after.AuthorizationPolicyID != before.AuthorizationPolicyID {
		t.Errorf("authorization changed %q -> %q; a refreshed preview must stay as private as it was created",
			before.AuthorizationPolicyID, after.AuthorizationPolicyID)
	}
	if after.StorageBucketID != before.StorageBucketID {
		t.Errorf("bucket changed %q -> %q", before.StorageBucketID, after.StorageBucketID)
	}
	if after.DisplayName != before.DisplayName {
		t.Errorf("display name changed %q -> %q", before.DisplayName, after.DisplayName)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("updatedDate did not advance (%v -> %v)", before.UpdatedAt, after.UpdatedAt)
	}
}

// TestIntegration_WriteFileKeepsTheRowIdentityEvenWhenContentMatchesASibling
// is the one that would fail if the stale handler comment were true: the final
// replacement deliberately writes content byte-identical to a DIFFERENT
// preview in the same bucket.
func TestIntegration_WriteFileKeepsTheRowIdentityEvenWhenContentMatchesASibling(t *testing.T) {
	client, _, bucketID := integrationTarget(t)
	ctx := context.Background()

	shared := pngBytes(t, color.RGBA{R: 255, A: 255})
	other := pngBytes(t, color.RGBA{B: 255, A: 255})

	target := createPreview(t, client, bucketID, shared)
	sibling := createPreview(t, client, bucketID, shared)

	before, err := client.FindByID(ctx, target)
	if err != nil || before == nil {
		t.Fatalf("FindByID(target) = %v, %v", before, err)
	}
	siblingBefore, err := client.FindByID(ctx, sibling)
	if err != nil || siblingBefore == nil {
		t.Fatalf("FindByID(sibling) = %v, %v", siblingBefore, err)
	}

	if _, err := client.WriteFile(ctx, target, bytes.NewReader(other)); err != nil {
		t.Fatalf("WriteFile with distinct content: %v", err)
	}
	// Back to content identical to `sibling`. A content-unique index would
	// reject exactly this write.
	if _, err := client.WriteFile(ctx, target, bytes.NewReader(shared)); err != nil {
		t.Fatalf("WriteFile with content identical to a sibling preview: %v — the collision premise would be back", err)
	}

	after, err := client.FindByID(ctx, target)
	if err != nil || after == nil {
		t.Fatalf("FindByID(target, after) = %v, %v", after, err)
	}
	assertRowIdentityPreserved(t, before, after)

	siblingAfter, err := client.FindByID(ctx, sibling)
	if err != nil || siblingAfter == nil {
		t.Fatalf("FindByID(sibling, after) = %v, %v", siblingAfter, err)
	}
	if !siblingAfter.UpdatedAt.Equal(siblingBefore.UpdatedAt) || siblingAfter.ExternalID != siblingBefore.ExternalID {
		t.Errorf("the sibling preview was disturbed by a write to another row: %+v -> %+v", siblingBefore, siblingAfter)
	}
}

// TestIntegration_RepeatedRefreshesAddNoLogicalRows is the regression that the
// superseded create-and-swap design would fail: every refresh there minted a
// new file and abandoned the old one.
func TestIntegration_RepeatedRefreshesAddNoLogicalRows(t *testing.T) {
	client, _, bucketID := integrationTarget(t)
	ctx := context.Background()

	id := createPreview(t, client, bucketID, pngBytes(t, color.RGBA{R: 255, A: 255}))

	shades := []color.RGBA{
		{G: 255, A: 255},
		{B: 255, A: 255},
		{R: 128, G: 128, A: 255},
	}
	for i, c := range shades {
		if _, err := client.WriteFile(ctx, id, bytes.NewReader(pngBytes(t, c))); err != nil {
			t.Fatalf("refresh %d: %v", i+1, err)
		}
		meta, err := client.FindByID(ctx, id)
		if err != nil || meta == nil {
			t.Fatalf("refresh %d FindByID = %v, %v", i+1, meta, err)
		}
		if meta.ID != id {
			t.Fatalf("refresh %d moved the row to %q; the preview fileID is supposed to be stable", i+1, meta.ID)
		}
	}
}

// TestIntegration_AMissingRowIs404EverywhereAndPutNeverCreates pins the
// distinction the render path depends on: a genuine 404 is the ONLY signal
// that permits creating a preview file.
func TestIntegration_AMissingRowIs404EverywhereAndPutNeverCreates(t *testing.T) {
	client, _, bucketID := integrationTarget(t)
	ctx := context.Background()

	// Create then delete, so the id is well-formed and certainly absent.
	ghost, err := client.CreatePreviewFile(ctx, bucketID, bytes.NewReader(pngBytes(t, color.RGBA{A: 255})))
	if err != nil {
		t.Fatalf("CreatePreviewFile: %v", err)
	}
	if err := client.DeletePreviewFile(ctx, ghost); err != nil {
		t.Fatalf("DeletePreviewFile: %v", err)
	}

	meta, err := client.FindByID(ctx, ghost)
	if err != nil {
		t.Errorf("FindByID on a deleted row error = %v, want a clean nil result", err)
	}
	if meta != nil {
		t.Errorf("FindByID on a deleted row = %+v, want nil", meta)
	}

	body, err := client.ReadFile(ctx, ghost)
	if err == nil {
		_ = body.Close()
		t.Error("ReadFile on a deleted row succeeded")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile error = %v, want fs.ErrNotExist — absence must be distinguishable from an upstream failure", err)
	}

	if _, err := client.WriteFile(ctx, ghost, bytes.NewReader(pngBytes(t, color.RGBA{A: 255}))); err == nil {
		t.Error("WriteFile resurrected a deleted row; PUT must never create")
	}
}

// TestIntegration_HeadIsNotRoutedOnTheContentPath is the regression test for
// the defect that motivated removing FileExists: file-service registers GET
// only, so a HEAD probe returns 405 — which an existence check reads as
// "absent", for every file that exists.
func TestIntegration_HeadIsNotRoutedOnTheContentPath(t *testing.T) {
	client, baseURL, bucketID := integrationTarget(t)
	id := createPreview(t, client, bucketID, pngBytes(t, color.RGBA{R: 255, A: 255}))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodHead, baseURL+"/internal/file/"+id+"/content", nil)
	if err != nil {
		t.Fatalf("build HEAD request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusOK {
		t.Fatal("HEAD is routed after all — re-evaluate whether a HEAD existence probe is now supportable")
	}
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("HEAD status = %d, want 405", resp.StatusCode)
	}

	// And the supported probe does confirm the very same file.
	body, err := client.ReadFile(context.Background(), id)
	if err != nil {
		t.Fatalf("GET content on the same id failed: %v", err)
	}
	defer func() { _ = body.Close() }()
	n, err := io.Copy(io.Discard, body)
	if err != nil {
		t.Fatalf("drain content: %v", err)
	}
	if n == 0 {
		t.Error("GET content returned an empty body for an existing preview")
	}
}
