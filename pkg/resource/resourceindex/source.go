package resourceindex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// logKeyResourceID is the structured-log key for a resource id.
const logKeyResourceID = "resource_id"

// BlobReader fetches resource content from blob storage. It is the same
// contract the resources/read middleware uses (resource.S3Client's read half),
// declared narrowly here so the consumer takes only the capability it needs.
type BlobReader interface {
	GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error)
}

// Source implements indexjobs.Source for the resources kind. A unit is one
// managed resource (SourceID = resource id) and yields exactly one item: the
// resource's composed index text (metadata plus, for text-family content, a
// bounded prefix extracted from its blob). The worker embeds it and the Sink
// writes the vector back onto the same row.
type Source struct {
	store  *Store
	blobs  BlobReader
	bucket string
}

// NewSource returns a Source backed by the given store. blobs and bucket locate
// the resource content; a nil reader (a deployment with no S3 connection
// configured for resources) indexes metadata only.
func NewSource(store *Store, blobs BlobReader, bucket string) *Source {
	return &Source{store: store, blobs: blobs, bucket: bucket}
}

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the resources source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns the resource's single embeddable item. A resource deleted
// between enqueue and claim reports indexjobs.ErrSourceGone, so the worker
// clears the unit and resolves the job instead of recording a failure.
func (s *Source) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	row, err := s.store.Load(ctx, sourceID)
	if errors.Is(err, errGone) {
		return nil, fmt.Errorf("resource %s: %w", sourceID, indexjobs.ErrSourceGone)
	}
	if err != nil {
		return nil, fmt.Errorf("resourceSource: load items: %w", err)
	}

	content := s.resolveContentText(ctx, sourceID, row)
	return []indexjobs.Item{{ItemID: sourceID, Text: resource.IndexText(row.Resource, content)}}, nil
}

// resolveContentText returns the text to index from the resource's content and
// settles the row's content state.
//
// There are three outcomes, and the difference between them is what keeps a
// failed extraction recoverable:
//
//   - Nothing to extract (no blob storage wired — in which case the upload path
//     stored no bytes either — or no key, a binary type, or an object too large
//     to pull whole). The row keeps whatever text it has and is SETTLED, so it
//     stops being a gap.
//   - Extracted, or the object is confirmed gone (a confirmed orphan clears the
//     stale text: that content is permanently unreachable and indexing it would
//     keep answering searches with text no reader can fetch). Written and
//     SETTLED.
//   - A transient blob failure. The previously extracted text is kept and the row
//     is deliberately left UNSETTLED, so FindGaps returns it again and the next
//     sweep retries. Leaving it settled here is the bug this shape exists to
//     prevent: the metadata embed succeeds either way, so a settled row would
//     drop out of the gap query with its file contents never indexed.
func (s *Source) resolveContentText(ctx context.Context, id string, row Row) string {
	if s.blobs == nil || row.Resource.S3Key == "" || !contenttype.IsTextual(row.Resource.MIMEType) {
		s.settleContent(ctx, id, row, row.ContentText)
		return row.ContentText
	}
	if row.Resource.SizeBytes > resource.MaxContentReadBytes {
		slog.Info("resource index: content too large to extract; indexing metadata only",
			logKeyResourceID, id, "size_bytes", row.Resource.SizeBytes) // #nosec G706 -- server-generated id and size, not user input
		s.settleContent(ctx, id, row, row.ContentText)
		return row.ContentText
	}

	body, _, err := s.blobs.GetObject(ctx, s.bucket, row.Resource.S3Key)
	switch {
	case err == nil:
	case resource.IsObjectNotFound(err):
		slog.Warn("resource index: backing object missing; indexing metadata only",
			logKeyResourceID, id) // #nosec G706 -- server-generated id, not user input
		s.settleContent(ctx, id, row, "")
		return ""
	default:
		slog.Warn("resource index: blob read failed; leaving the content pass owed for the next sweep",
			logKeyResourceID, id, "error", err) //nolint:gosec // structured slog of a store error
		return row.ContentText
	}

	extracted := extractText(body, resource.MaxContentIndexBytes)
	s.settleContent(ctx, id, row, extracted)
	return extracted
}

// settleContent records the resolved text and marks the content pass done. The
// write is skipped only when the row is ALREADY settled and the text is
// unchanged — the steady-state re-index — so a row that has never settled is
// always stamped, including when its extracted text is legitimately empty.
//
// A write failure is logged and swallowed rather than failing the job: the text
// just resolved is still indexed for this pass, and because the row stays
// unsettled, FindGaps returns it again and the next sweep retries the write.
func (s *Source) settleContent(ctx context.Context, id string, row Row, next string) {
	if row.ContentSettled && row.ContentText == next {
		return
	}
	if err := s.store.SetContentText(ctx, id, next); err != nil {
		slog.Warn("resource index: persisting extracted content failed",
			logKeyResourceID, id, "error", err) //nolint:gosec // structured slog of a store error
	}
}

// extractText returns the indexable text prefix of raw content: at most limit
// bytes, cut at a rune boundary, with NUL bytes and invalid UTF-8 removed.
// Postgres TEXT cannot hold a NUL byte, and a file whose declared type is
// textual can still carry stray binary, so a resource with one bad byte must not
// become permanently unindexable.
func extractText(body []byte, limit int) string {
	if len(body) > limit {
		body = body[:limit]
		// Drop a partial trailing rune left by the cut. At most UTFMax-1 bytes can
		// belong to one, so the trim is bounded rather than scanning the prefix.
		for i := 0; i < utf8.UTFMax-1 && len(body) > 0; i++ {
			r, size := utf8.DecodeLastRune(body)
			if r != utf8.RuneError || size != 1 {
				break
			}
			body = body[:len(body)-1]
		}
	}
	text := strings.ToValidUTF8(string(body), "")
	return strings.ReplaceAll(text, "\x00", "")
}

// OnSucceeded is a no-op: the ranked search reads embeddings from the resources
// table directly on every query, so there is no in-memory cache to refresh after
// a backfill writes a vector.
func (*Source) OnSucceeded(string) {}
