package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// An endpoint has no catalog entity of its own to hang a saved query on the way
// a dataset does, so what a proven API call becomes is an example on the
// endpoint: a request known to have worked, shown to the next agent that reads
// that endpoint's schema (#1321).
//
// Examples are keyed by connection rather than by catalog. A spec is shared;
// evidence is not. Two connections can serve the same spec and disagree about
// what a working request looks like, and an example promoted against one of
// them says nothing about the other.

// ErrInvalidExample is returned when an example names no endpoint to belong to.
var ErrInvalidExample = errors.New("catalog: an example needs a connection and a name")

// maxExamplesPerEndpoint bounds how many saved examples one endpoint returns.
// The examples are read into an agent's context beside the endpoint's schema,
// which is already the largest thing that tool returns.
const maxExamplesPerEndpoint = 5

// Example is one endpoint invocation worth keeping.
type Example struct {
	ID          string `json:"id,omitempty"`
	Connection  string `json:"connection,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Method      string `json:"method,omitempty"`
	Path        string `json:"path,omitempty"`
	// Name is what the example is called: the purpose stated for the call it
	// was promoted from.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// CallRecordID leads back to the recorded call this was promoted from.
	CallRecordID string `json:"call_record_id,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
}

// ExampleStore saves and reads endpoint examples.
//
// It is deliberately separate from Store rather than added to it: Store is the
// API catalog's own CRUD contract with several implementations, and examples
// are a different concern with a different key. A deployment reaches whichever
// it needs.
type ExampleStore interface {
	// SaveExample stores an example, replacing an earlier one with the same
	// name on the same endpoint. Returns the example's id.
	SaveExample(ctx context.Context, ex Example) (string, error)
	// ListExamples returns the saved examples for one endpoint, newest first.
	ListExamples(ctx context.Context, connection, operationID string) ([]Example, error)
}

// saveExampleQuery upserts on (connection, operation_id, name): promoting the
// same purpose twice refreshes the example rather than accumulating near
// duplicates the reader has to choose between.
const saveExampleQuery = `
	INSERT INTO api_endpoint_examples
		(connection, operation_id, method, path, name, description, call_record_id, created_by)
	VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::uuid, $8)
	ON CONFLICT (connection, operation_id, name) DO UPDATE
	SET method = EXCLUDED.method,
	    path = EXCLUDED.path,
	    description = EXCLUDED.description,
	    call_record_id = EXCLUDED.call_record_id,
	    created_by = EXCLUDED.created_by,
	    updated_at = NOW()
	RETURNING id`

// SaveExample stores one endpoint example.
func (s *PostgresStore) SaveExample(ctx context.Context, ex Example) (string, error) {
	name := strings.TrimSpace(ex.Name)
	if ex.Connection == "" || name == "" {
		return "", ErrInvalidExample
	}
	var id string
	err := s.db.QueryRowContext(ctx, saveExampleQuery,
		ex.Connection, ex.OperationID, ex.Method, ex.Path, name, ex.Description,
		ex.CallRecordID, ex.CreatedBy,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("saving endpoint example: %w", err)
	}
	return id, nil
}

// listExamplesQuery reads one endpoint's examples, newest first.
const listExamplesQuery = `
	SELECT id, connection, operation_id, method, path, name, description,
	       COALESCE(call_record_id::text, ''), created_by
	FROM api_endpoint_examples
	WHERE connection = $1 AND operation_id = $2
	ORDER BY updated_at DESC
	LIMIT $3`

// ListExamples returns the saved examples for one endpoint.
func (s *PostgresStore) ListExamples(ctx context.Context, connection, operationID string) ([]Example, error) {
	if connection == "" || operationID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, listExamplesQuery, connection, operationID, maxExamplesPerEndpoint)
	if err != nil {
		return nil, fmt.Errorf("listing endpoint examples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	examples := []Example{}
	for rows.Next() {
		var ex Example
		if err := rows.Scan(&ex.ID, &ex.Connection, &ex.OperationID, &ex.Method, &ex.Path,
			&ex.Name, &ex.Description, &ex.CallRecordID, &ex.CreatedBy); err != nil {
			return nil, fmt.Errorf("scanning endpoint example: %w", err)
		}
		examples = append(examples, ex)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating endpoint examples: %w", err)
	}
	return examples, nil
}

// MemoryExampleStore is the no-database example store: a deployment that runs
// the API gateway from file configuration alone still resolves endpoints, and
// an example promoted there lives as long as the process does.
type MemoryExampleStore struct {
	mu       sync.RWMutex
	examples map[string][]Example
	nextID   int
}

// NewMemoryExampleStore returns an in-memory example store.
func NewMemoryExampleStore() *MemoryExampleStore {
	return &MemoryExampleStore{examples: map[string][]Example{}}
}

// SaveExample stores an example, replacing one of the same name.
func (m *MemoryExampleStore) SaveExample(_ context.Context, ex Example) (string, error) {
	name := strings.TrimSpace(ex.Name)
	if ex.Connection == "" || name == "" {
		return "", ErrInvalidExample
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ex.Name = name
	key := ex.Connection + "\x00" + ex.OperationID
	for i, existing := range m.examples[key] {
		if existing.Name == name {
			ex.ID = existing.ID
			m.examples[key][i] = ex
			return ex.ID, nil
		}
	}
	m.nextID++
	ex.ID = fmt.Sprintf("example-%d", m.nextID)
	m.examples[key] = append(m.examples[key], ex)
	return ex.ID, nil
}

// ListExamples returns the saved examples for one endpoint, newest first.
func (m *MemoryExampleStore) ListExamples(_ context.Context, connection, operationID string) ([]Example, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stored := m.examples[connection+"\x00"+operationID]
	examples := make([]Example, len(stored))
	copy(examples, stored)
	sort.SliceStable(examples, func(i, j int) bool { return examples[i].ID > examples[j].ID })
	if len(examples) > maxExamplesPerEndpoint {
		examples = examples[:maxExamplesPerEndpoint]
	}
	return examples, nil
}

// Verify interface compliance.
var (
	_ ExampleStore = (*PostgresStore)(nil)
	_ ExampleStore = (*MemoryExampleStore)(nil)
)
