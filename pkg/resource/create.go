package resource

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// NewResource is a resource about to exist: where it is filed, what it is
// called, and the bytes it starts life with.
//
// Every field is already validated by the time it reaches CreateResource. The
// caller owns validation because the two surfaces read their input from
// different places -- a multipart form, a tool call -- and reporting a bad
// folder path as a form error or as a tool result is theirs to decide.
type NewResource struct {
	Scope       Scope
	ScopeID     string
	Path        string
	Filename    string
	DisplayName string
	Description string
	Tags        []string
	// Data is the content, and MIMEType the type it is stored under.
	Data     []byte
	MIMEType string
	// DeclaredMIMEType is what the caller said the bytes were, kept so a
	// detection that replaced it is recorded. Empty when nothing was declared.
	DeclaredMIMEType string
}

// CreateResource stores the blob, inserts the metadata row, and records the
// content as version 1 so the trail starts at the upload rather than at the
// first revision. A metadata failure removes the blob, so a failed create
// leaves nothing behind.
//
// It is exported for the same reason ReviseContent is: creating a managed
// resource is not the browser's alone. An agent, and therefore a scheduled
// script, creates one through here (#1487), so a resource written on the
// platform's own initiative is the same record as one a person uploaded --
// same URI, same version trail, same retention.
func CreateResource(ctx context.Context, deps Deps, claims *Claims, in NewResource) (*Resource, error) {
	id, err := GenerateID()
	if err != nil {
		return nil, fmt.Errorf("generating ID: %w", err)
	}

	scheme := deps.URIScheme
	if scheme == "" {
		scheme = DefaultURIScheme
	}
	uri := BuildURI(scheme, in.Scope, in.ScopeID, in.Path, in.Filename)
	s3Key := BuildS3Key(in.Scope, in.ScopeID, id, in.Filename)

	// A stored type that disagrees with what the client sent is the one thing
	// an operator cannot reconstruct after the fact, so record the swap. Both
	// types trace back to a client-supplied value, so both are sanitized
	// before they reach the log.
	if in.MIMEType != in.DeclaredMIMEType {
		slog.Info("resource upload: content type detected from content",
			logKeyResourceID, id,
			"declared_mime_type", logsan.SanitizeForLog(in.DeclaredMIMEType),
			"stored_mime_type", logsan.SanitizeForLog(in.MIMEType),
		)
	}

	// The subject is the principal that made the call and the address is the
	// person whose authority it ran under. They are the same for a person, and
	// for a managed-script run they are script:<name> and its version author --
	// the pairing every other surface records for a run, and the one the
	// version trail has to carry or a scheduled refresh reads as having been
	// made by whoever happens to own the script.
	res := Resource{
		ID: id, Scope: in.Scope, ScopeID: in.ScopeID,
		Path: in.Path, Filename: in.Filename,
		DisplayName: in.DisplayName, Description: in.Description,
		MIMEType: in.MIMEType, SizeBytes: int64(len(in.Data)),
		S3Key: s3Key, URI: uri, Tags: in.Tags,
		UploaderSub: claims.Sub, UploaderEmail: PersonAddress(*claims),
	}

	if deps.S3Client != nil {
		if err := deps.S3Client.PutObject(ctx, deps.S3Bucket, s3Key, in.Data, in.MIMEType); err != nil {
			slog.Error("resource upload: s3 put failed", msgError, err)
			return nil, &storageError{msg: msgStorageRefused}
		}
	}

	if err := deps.Store.Insert(ctx, res); err != nil {
		// Clean up orphaned S3 blob.
		if deps.S3Client != nil {
			_ = deps.S3Client.DeleteObject(ctx, deps.S3Bucket, s3Key)
		}
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, &conflictError{msg: "a resource with this library, folder, and filename already exists"}
		}
		slog.Error("resource upload: db insert failed", msgError, err)
		return nil, fmt.Errorf("saving resource metadata: %w", err)
	}

	saved := readBackCreated(ctx, deps, id, res)
	recordInitialVersion(ctx, deps, saved, claims)
	noteProducer(ctx, deps, claims, producedby.Write{TargetID: saved.ID, Created: true, Version: 1})
	return saved, nil
}

// readBackCreated re-reads the inserted row so the caller sees the stored
// timestamps. A read that fails falls back to the record as written, stamped
// now: the resource exists either way, and reporting the create as a failure
// because the read-back failed would be a lie about what happened.
func readBackCreated(ctx context.Context, deps Deps, id string, written Resource) *Resource {
	saved, err := deps.Store.Get(ctx, id)
	if err != nil || saved == nil {
		now := time.Now().UTC()
		written.CreatedAt = now
		written.UpdatedAt = now
		return &written
	}
	return saved
}

// recordInitialVersion records the created resource as version 1 so the trail
// starts at upload rather than at the first revision. A failure is logged, not
// surfaced: the upload succeeded and the resource is usable; the migration's
// backfill shape (a v1 row derived from the resource row) is exactly what a
// later repair would write.
func recordInitialVersion(ctx context.Context, deps Deps, res *Resource, claims *Claims) {
	if deps.Versions == nil {
		return
	}
	if _, err := deps.Versions.AddRevision(ctx, Revision{
		ResourceID:    res.ID,
		MIMEType:      res.MIMEType,
		SizeBytes:     res.SizeBytes,
		S3Key:         res.S3Key,
		UploaderSub:   claims.Sub,
		UploaderEmail: PersonAddress(*claims),
	}); err != nil {
		slog.Warn("resource upload: recording initial version failed", msgError, err,
			logKeyResourceID, res.ID) // #nosec G706 -- server-generated ID
	}
}
