package catalog

import (
	"context"
	"time"
)

// SchemaDocument is a connection's schema document as the catalog scan last
// read it: the OpenAPI spec an API connection's operations come from, the SDL
// a GraphQL connection's operations come from (#1918). It belongs to its
// connection, so it is read under the same connection boundary as the
// connection's datasets.
type SchemaDocument struct {
	ID             string `json:"id"`
	Reference      string `json:"reference"`
	ConnectionKind string `json:"connection_kind"`
	Connection     string `json:"connection"`
	Filename       string `json:"filename"`
	MIMEType       string `json:"mime_type"`
	SizeBytes      int    `json:"size_bytes"`
	// Version counts the times the document's content changed; a scan that
	// reads the same content again does not move it.
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	// Content is the document itself. A listing leaves it empty.
	Content []byte `json:"-"`
}

// DocumentInput is a schema document as a scan read it.
type DocumentInput struct {
	ConnectionKind string
	Connection     string
	Filename       string
	MIMEType       string
	Content        []byte
}

// DocumentStore keeps connections' schema documents.
type DocumentStore interface {
	// PutDocument stores a document, recording a new version only when its
	// content or media type changed, and reports whether it did.
	PutDocument(ctx context.Context, in DocumentInput) (*SchemaDocument, bool, error)
	// GetDocument reads one document with its content.
	GetDocument(ctx context.Context, ref Ref) (*SchemaDocument, error)
	// ListDocuments lists every document without its content, by connection
	// and filename.
	ListDocuments(ctx context.Context) ([]SchemaDocument, error)
}
