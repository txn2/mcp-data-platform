package catalogstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var _ catalog.DocumentStore = (*Store)(nil)

// upsertDocument stores a document. The version moves, and the content is
// replaced, only when the content or media type differs from what is held; a
// document already held as given returns no row.
const upsertDocument = `
INSERT INTO catalog_documents (connection_kind, connection_name, filename, mime_type, content, content_sha256)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (connection_kind, connection_name, filename) DO UPDATE SET
    mime_type = EXCLUDED.mime_type,
    content = EXCLUDED.content,
    content_sha256 = EXCLUDED.content_sha256,
    version = catalog_documents.version + 1,
    updated_at = NOW()
WHERE catalog_documents.content_sha256 <> EXCLUDED.content_sha256
   OR catalog_documents.mime_type <> EXCLUDED.mime_type
RETURNING id, version, updated_at`

const selectDocumentMeta = `
SELECT id, mime_type, octet_length(content), version, updated_at
FROM catalog_documents
WHERE connection_kind = $1 AND connection_name = $2 AND filename = $3`

const selectDocument = `
SELECT id, mime_type, content, version, updated_at
FROM catalog_documents
WHERE connection_kind = $1 AND connection_name = $2 AND filename = $3`

const listDocuments = `
SELECT id, connection_kind, connection_name, filename, mime_type, octet_length(content), version, updated_at
FROM catalog_documents
ORDER BY connection_kind, connection_name, filename`

// PutDocument stores a schema document, recording a new version only when its
// content or media type changed.
func (s *Store) PutDocument(ctx context.Context, in catalog.DocumentInput) (*catalog.SchemaDocument, bool, error) {
	ref := catalog.SchemaDocumentRef(in.ConnectionKind, in.Connection, in.Filename)
	if err := ref.Validate(); err != nil {
		return nil, false, fmt.Errorf("schema document: %w", err)
	}
	if in.MIMEType == "" {
		return nil, false, errors.New("a schema document needs a media type")
	}
	sum := sha256.Sum256(in.Content)
	doc := &catalog.SchemaDocument{
		Reference: ref.String(), ConnectionKind: in.ConnectionKind, Connection: in.Connection,
		Filename: in.Filename, MIMEType: in.MIMEType, SizeBytes: len(in.Content),
	}
	err := s.db.QueryRowContext(ctx, upsertDocument,
		in.ConnectionKind, in.Connection, in.Filename, in.MIMEType, in.Content, hex.EncodeToString(sum[:]),
	).Scan(&doc.ID, &doc.Version, &doc.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		// The content is what is held: the conditional update wrote nothing.
		held, err := s.documentMeta(ctx, ref)
		if err != nil {
			return nil, false, err
		}
		return held, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("storing schema document: %w", err)
	}
	return doc, true, nil
}

// documentMeta reads a document's record without its content.
func (s *Store) documentMeta(ctx context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error) {
	doc := documentOf(ref)
	err := s.db.QueryRowContext(ctx, selectDocumentMeta, ref.ConnectionKind, ref.Connection, ref.Key).
		Scan(&doc.ID, &doc.MIMEType, &doc.SizeBytes, &doc.Version, &doc.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("reading schema document: %w", err)
	}
	return doc, nil
}

// GetDocument reads one schema document with its content.
func (s *Store) GetDocument(ctx context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error) {
	if ref.Kind != catalog.KindSchemaDocument {
		return nil, fmt.Errorf("want a schema document reference, got %s: %w", ref.Kind, catalog.ErrInvalidRef)
	}
	doc := documentOf(ref)
	err := s.db.QueryRowContext(ctx, selectDocument, ref.ConnectionKind, ref.Connection, ref.Key).
		Scan(&doc.ID, &doc.MIMEType, &doc.Content, &doc.Version, &doc.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading schema document: %w", err)
	}
	doc.SizeBytes = len(doc.Content)
	return doc, nil
}

// ListDocuments lists every schema document without its content.
func (s *Store) ListDocuments(ctx context.Context) ([]catalog.SchemaDocument, error) {
	rows, err := s.db.QueryContext(ctx, listDocuments)
	if err != nil {
		return nil, fmt.Errorf("listing schema documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]catalog.SchemaDocument, 0)
	for rows.Next() {
		var d catalog.SchemaDocument
		if err := rows.Scan(&d.ID, &d.ConnectionKind, &d.Connection, &d.Filename, &d.MIMEType,
			&d.SizeBytes, &d.Version, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning schema document: %w", err)
		}
		d.Reference = catalog.SchemaDocumentRef(d.ConnectionKind, d.Connection, d.Filename).String()
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing schema documents: %w", err)
	}
	return out, nil
}

// documentOf is the identity half of a document record.
func documentOf(ref catalog.Ref) *catalog.SchemaDocument {
	return &catalog.SchemaDocument{
		Reference: ref.String(), ConnectionKind: ref.ConnectionKind, Connection: ref.Connection, Filename: ref.Key,
	}
}
