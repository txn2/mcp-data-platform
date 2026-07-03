package memory

import (
	"context"
	"errors"
	"strings"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

// AutoCaptureInput is a server-initiated (non-agent) capture. Unlike the
// memory_capture tool, there is no incoming request to read identity from, so
// the caller supplies it explicitly. Source defaults to automation so audits
// and reads can distinguish platform-minted records from agent- and user-
// authored ones.
type AutoCaptureInput struct {
	SinkClass  string
	Content    string
	Category   string
	Source     string
	Confidence string
	EntityURNs []string
	Metadata   map[string]any

	// CreatedBy is the owner email the record is scoped to (required). Persona,
	// UserID, and SessionID mirror the tool path's PlatformContext fields.
	CreatedBy string
	Persona   string
	UserID    string
	SessionID string
}

// CaptureResult is the outcome of a server-initiated capture.
type CaptureResult struct {
	ID         string
	SinkClass  string
	Status     string
	Superseded []string
}

// AutoCapture persists a server-initiated capture, reusing the full
// memory_capture pipeline: sink-class routing (live vs reviewed), embedding,
// recall-first supersede, and the pending-insight overlay for reviewed classes.
// It is the single entry point for platform-minted memory (e.g. reflexive
// capture of a query error and its later fix), so such records go through the
// same review and dedup path as agent captures rather than a parallel writer.
func (t *Toolkit) AutoCapture(ctx context.Context, in AutoCaptureInput) (*CaptureResult, error) {
	content := strings.TrimSpace(in.Content)
	for _, err := range []error{
		memstore.ValidateSinkClass(in.SinkClass),
		memstore.ValidateContent(content),
		memstore.ValidateEntityURNs(in.EntityURNs),
		memstore.ValidateCategory(in.Category),
		memstore.ValidateConfidence(in.Confidence),
		memstore.ValidateSource(in.Source),
	} {
		if err != nil {
			return nil, err
		}
	}
	if in.CreatedBy == "" {
		return nil, errors.New("created_by (owner email) is required")
	}

	id, err := generateID()
	if err != nil {
		return nil, err
	}

	source := in.Source
	if source == "" {
		source = memstore.SourceAutomation
	}

	actor := captureActor{UserID: in.UserID, Email: in.CreatedBy, Persona: in.Persona, SessionID: in.SessionID}
	rec := memstore.Record{
		ID:         id,
		CreatedBy:  in.CreatedBy,
		Persona:    in.Persona,
		Dimension:  memstore.SinkClassDimension(in.SinkClass),
		SinkClass:  in.SinkClass,
		Content:    content,
		Category:   memstore.NormalizeCategory(in.Category),
		Confidence: memstore.NormalizeConfidence(in.Confidence),
		Source:     source,
		EntityURNs: in.EntityURNs,
		Status:     memstore.StatusActive,
		Metadata:   captureMetadata(in.SinkClass, in.SessionID, nil, in.Metadata),
	}

	out, err := t.applyCapture(ctx, &rec, in.SinkClass, actor, nil)
	if err != nil {
		return nil, err
	}

	return &CaptureResult{
		ID:         rec.ID,
		SinkClass:  rec.SinkClass,
		Status:     rec.Status,
		Superseded: out.Superseded,
	}, nil
}
