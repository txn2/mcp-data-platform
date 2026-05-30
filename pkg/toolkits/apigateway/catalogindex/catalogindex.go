// Package catalogindex is the api-catalog adapter onto the generic
// indexjobs framework. It packs (catalog_id, spec_name) into the
// framework's opaque source_id, implements the indexjobs.Sink
// against the existing api_catalog_operation_embeddings table
// (preserving that table's FK + ON DELETE CASCADE), and provides an
// AdminStore whose api-catalog-shaped types and methods are a
// drop-in for the admin handler that previously talked to the
// per-toolkit embedjobs queue.
//
// The package deliberately does NOT import the apigateway toolkit
// (only catalog + indexjobs + database/sql), so pkg/admin can depend
// on it for the AdminStore types without pulling the toolkit's
// transitive dependencies into the admin import surface. The
// api-catalog Source (which needs the toolkit's OpenAPI parser)
// lives in the platform package instead.
//
//nolint:revive // max-public-structs: this package's exported surface is one cohesive api-catalog adapter (the admin DTOs SpecKey/Job/ListFilter/SpecStatusRow/CatalogHealth that mirror the prior queue's types, plus the Sink/AdminStore/Store contracts), not a heap of unrelated types.
package catalogindex

import (
	"context"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
)

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "api_catalog"

// sourceIDDelim separates the catalog id from the spec name inside
// the framework's opaque source_id. The unit-separator control
// character cannot appear in a catalog id (slug-validated) or a
// spec name (filename-validated), so EncodeSourceID is injective and
// DecodeSourceID's split is unambiguous. It is also SQL-expressible
// as E'\x1f' for the admin joins.
const sourceIDDelim = "\x1f"

// EncodeSourceID packs a (catalog_id, spec_name) pair into the
// indexjobs source_id.
func EncodeSourceID(catalogID, specName string) string {
	return catalogID + sourceIDDelim + specName
}

// DecodeSourceID splits a source_id back into its (catalog_id,
// spec_name) pair. ok is false when the id does not contain the
// delimiter (a malformed or foreign-kind id), so callers can skip
// rather than mis-attribute it.
func DecodeSourceID(sourceID string) (catalogID, specName string, ok bool) {
	cat, spec, found := strings.Cut(sourceID, sourceIDDelim)
	if !found {
		return "", "", false
	}
	return cat, spec, true
}

// sourceIDPrefix is the LIKE-prefix that matches every source_id
// under one catalog. The trailing delimiter keeps catalog "foo" from
// matching catalog "foobar".
func sourceIDPrefix(catalogID string) string {
	return catalogID + sourceIDDelim
}

// ErrNotFound is returned by AdminStore.Get when no job with the id
// exists. Aliased to the framework sentinel so callers' errors.Is
// checks work against either.
var ErrNotFound = indexjobs.ErrNotFound

// Kind identifies what enqueued a job, in the api-catalog vocabulary
// the admin UI renders. It maps one-for-one onto an indexjobs.Trigger.
type Kind string

// Job kinds, matching the labels the admin job-history view shows.
const (
	KindSpecWrite   Kind = "spec_write"
	KindReconciler  Kind = "reconciler"
	KindManualRetry Kind = "manual_retry"
)

// toTrigger maps an api-catalog Kind to the framework trigger.
// Unknown kinds default to a write trigger.
func (k Kind) toTrigger() indexjobs.Trigger {
	switch k {
	case KindReconciler:
		return indexjobs.TriggerReconciler
	case KindManualRetry:
		return indexjobs.TriggerManualRetry
	case KindSpecWrite:
		return indexjobs.TriggerWrite
	default:
		return indexjobs.TriggerWrite
	}
}

// kindFromTrigger maps a framework trigger back to the api-catalog
// Kind for display.
func kindFromTrigger(t indexjobs.Trigger) Kind {
	switch t {
	case indexjobs.TriggerReconciler:
		return KindReconciler
	case indexjobs.TriggerManualRetry:
		return KindManualRetry
	case indexjobs.TriggerWrite:
		return KindSpecWrite
	default:
		return KindSpecWrite
	}
}

// Status mirrors indexjobs.Status in the api-catalog vocabulary.
type Status string

// Job state values, identical to the framework's.
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// SpecKey is the (catalog_id, spec_name) composite the admin handler
// passes to Enqueue.
type SpecKey struct {
	CatalogID string
	SpecName  string
}

// ListFilter narrows an admin job-list query in api-catalog terms.
// Zero-value fields are ignored. The AdminStore translates it to an
// indexjobs.ListFilter (encoding the source_id / prefix).
type ListFilter struct {
	CatalogID string
	SpecName  string
	Status    Status
	Kind      Kind
	Limit     int
}

// Job is one admin-facing job row in api-catalog terms. Field set
// matches the embedjobs.Job the admin handler rendered before the
// generalization so the response builders are unchanged.
type Job struct {
	ID             int64
	CatalogID      string
	SpecName       string
	Kind           Kind
	Status         Status
	Attempts       int
	LastError      string
	NextRunAt      time.Time
	WorkerID       string
	LeaseExpiresAt *time.Time
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
	EmbeddedSoFar  int
}

// SpecStatusRow is the per-spec embedding status the admin
// embedding-status endpoint renders: how many operations the spec
// parses to, how many vectors are persisted, and the most recent
// job's state.
type SpecStatusRow struct {
	CatalogID      string
	SpecName       string
	OperationCount int
	EmbeddingCount int
	JobStatus      Status
	JobAttempts    int
	JobLastError   string
	JobUpdatedAt   *time.Time
	EmbeddedSoFar  int
}

// CatalogHealth is the per-catalog roll-up the admin embedding-health
// endpoint renders.
type CatalogHealth struct {
	CatalogID    string
	SpecsTotal   int
	SpecsIndexed int
	SpecsPending int
	SpecsRunning int
	SpecsFailed  int
}

// Store is the api-catalog-shaped queue surface the admin handler
// consumes. It is a drop-in for the prior embedjobs.Store subset;
// the AdminStore implementation backs it with index_jobs joined to
// the api_catalog tables.
type Store interface {
	Enqueue(ctx context.Context, key SpecKey, kind Kind) (bool, error)
	List(ctx context.Context, filter ListFilter) ([]Job, error)
	Get(ctx context.Context, id int64) (*Job, error)
	SpecStatuses(ctx context.Context, catalogID string) ([]SpecStatusRow, error)
	Health(ctx context.Context, catalogID string) (*CatalogHealth, error)
}
