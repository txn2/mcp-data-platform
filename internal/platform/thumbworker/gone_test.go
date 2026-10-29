package thumbworker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// captureLogs routes the default logger to a buffer at DEBUG for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestDrawResource_AFileDeletedWhileDrawnEndsQuietly holds #2367: a resource
// deleted after its tile was claimed is not a fault. Recording the tile finds
// no row, the attempt ends at DEBUG with no ERROR line, and the tiles stored
// for it are removed, since no row will name them.
func TestDrawResource_AFileDeletedWhileDrawnEndsQuietly(t *testing.T) {
	logs := captureLogs(t)
	blobs := newBlobs()
	r := resource.Resource{ID: "r4", MIMEType: "text/markdown", S3Key: "resources/r4/notes.md"}
	blobs.objects["b/"+r.S3Key] = []byte("# notes")
	res := &fakeResources{setErr: fmt.Errorf("resource %s deleted: %w", r.ID, resource.ErrNoSuchResource)}

	err := New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if err != nil {
		t.Fatalf("drawResource = %v, want the attempt finished", err)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("a deleted resource logged an ERROR:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "deleted while its tile was drawn") {
		t.Errorf("the quiet end was not logged at DEBUG:\n%s", logs)
	}
	want := []string{
		"b/" + resource.ThumbnailKeyFor(r.S3Key, resource.ThumbnailVariantLight),
		"b/" + resource.ThumbnailKeyFor(r.S3Key, resource.ThumbnailVariantDark),
	}
	slices.Sort(blobs.deleted)
	slices.Sort(want)
	if !slices.Equal(blobs.deleted, want) {
		t.Errorf("deleted %v, want the tiles drawn for the deleted file %v", blobs.deleted, want)
	}
	if len(res.holds) != 0 {
		t.Errorf("holds = %v; there is no row left to hold", res.holds)
	}
}

// TestDrawResource_AFailureOnADeletedFileEndsQuietly holds #2367 for the
// other write: the document's own failure finds no row to record against.
func TestDrawResource_AFailureOnADeletedFileEndsQuietly(t *testing.T) {
	logs := captureLogs(t)
	blobs := newBlobs()
	r := resource.Resource{ID: "r5", MIMEType: "text/html", S3Key: "resources/r5/x.html"}
	blobs.objects["b/"+r.S3Key] = []byte("<p>x</p>")
	res := &fakeResources{failErr: fmt.Errorf("resource %s deleted: %w", r.ID, resource.ErrNoSuchResource)}

	d := &fakeDrawer{results: []error{errors.New("headless: boom")}}
	if err := New(Tuning{}, Deps{Drawer: d, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r); err != nil {
		t.Fatalf("drawResource = %v", err)
	}
	if strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("a deleted resource logged an ERROR:\n%s", logs)
	}
	if len(blobs.deleted) != 0 {
		t.Errorf("deleted %v; nothing was stored", blobs.deleted)
	}
}

// TestDrawResource_ARemovalThatFailsIsWarned covers the tile left behind when
// storage refuses the removal: it is named at WARN, not ERROR.
func TestDrawResource_ARemovalThatFailsIsWarned(t *testing.T) {
	logs := captureLogs(t)
	blobs := newBlobs()
	blobs.delErr = errors.New("storage down")
	r := resource.Resource{ID: "r6", MIMEType: "text/html", S3Key: "resources/r6/x.html"}
	blobs.objects["b/"+r.S3Key] = []byte("<p>x</p>")
	res := &fakeResources{setErr: fmt.Errorf("resource %s deleted: %w", r.ID, resource.ErrNoSuchResource)}

	_ = New(Tuning{}, Deps{Drawer: &fakeDrawer{}, Resources: res, ResourceBlobs: blobs, ResourceBucket: "b"}).drawResource(context.Background(), r)
	if !strings.Contains(logs.String(), "removing a deleted resource's tile failed") || strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("logs:\n%s", logs)
	}
}
