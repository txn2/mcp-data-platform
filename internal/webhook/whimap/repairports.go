package whimap

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The ports the attachment repair (#2836) works through.

// StoredFile is one file under a source's attachments folder.
type StoredFile struct {
	ID     string
	Folder string
	Name   string
	Size   int64
}

// AttachmentLibrary is the source's attachments folder in its library.
type AttachmentLibrary interface {
	// ListAttachments lists every file under the source's attachments folder.
	ListAttachments(ctx context.Context, src whsource.Source) ([]StoredFile, error)
	// MoveFolder renames a folder of the source's library, aliasing each
	// file's old address.
	MoveFolder(ctx context.Context, src whsource.Source, from, to string) error
	// RemoveCopy removes the file id and its versions, after handing its
	// addresses to the file keep, so a reference to the copy resolves to the
	// file that stays (#2842).
	RemoveCopy(ctx context.Context, src whsource.Source, id, keep string) error
	// DeleteFolder removes an emptied folder and the folders beneath it.
	DeleteFolder(ctx context.Context, src whsource.Source, folder string) error
}

// EventReader reads the events of a source as compaction keeps them: one per
// event id, the earliest.
type EventReader interface {
	Live(ctx context.Context, source string) ([]whstore.Window, error)
	WindowEvents(ctx context.Context, src whsource.Source, w whstore.Window) ([]whevent.Stored, error)
}

// RepairStore records each source's repair.
type RepairStore interface {
	// RepairedVersion is the version of the last repair recorded for source, 0
	// when none has run.
	RepairedVersion(ctx context.Context, source string) (int, error)
	// RecordRepair records a repair of source by version.
	RecordRepair(ctx context.Context, source string, version int, r RepairReport) error
}
