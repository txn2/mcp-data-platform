package agentinstructions

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/configstore"
)

const testKey = "server.agent_instructions"

// fakeConfigStore is a configstore.Store whose reads and writes the tests
// control: mode selects file vs database, getErr forces a lookup failure, and
// written records what Set received.
type fakeConfigStore struct {
	mode    string
	entries map[string]string
	getErr  error
	setErr  error
	written []string
	authors []string
}

func newFakeStore() *fakeConfigStore {
	return &fakeConfigStore{mode: configModeDatabase, entries: map[string]string{}}
}

func (f *fakeConfigStore) Get(_ context.Context, key string) (*configstore.Entry, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.entries[key]
	if !ok {
		return nil, configstore.ErrNotFound
	}
	return &configstore.Entry{Key: key, Value: v, UpdatedAt: time.Now()}, nil
}

func (f *fakeConfigStore) Set(_ context.Context, key, value, author string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.entries[key] = value
	f.written = append(f.written, value)
	f.authors = append(f.authors, author)
	return nil
}

func (f *fakeConfigStore) Delete(_ context.Context, key, _ string) error {
	delete(f.entries, key)
	return nil
}

func (*fakeConfigStore) List(context.Context) ([]configstore.Entry, error) { return nil, nil }

func (*fakeConfigStore) Changelog(context.Context, int, int) ([]configstore.ChangelogEntry, int, error) {
	return nil, 0, nil
}

func (f *fakeConfigStore) Mode() string { return f.mode }

func TestNewRefusesAStoreThatCannotHoldAWrite(t *testing.T) {
	if got := New(nil, nil, testKey); got != nil {
		t.Errorf("New(nil store) = %v, want nil", got)
	}
	fileStore := newFakeStore()
	fileStore.mode = "file"
	if got := New(fileStore, nil, testKey); got != nil {
		t.Errorf("New(file store) = %v, want nil", got)
	}
}

// A nil Store must convert to a nil InstructionsStore at the consumer, or the
// sink's "not configured" branch never runs. The conversion is what makes the
// locally-declared interface safe.
func TestNilStoreConvertsToANilInterface(t *testing.T) {
	var consumed interface {
		AgentInstructions(context.Context) (string, error)
		SetAgentInstructions(context.Context, string, string) error
	} = New(nil, nil, testKey)
	if consumed != nil {
		t.Errorf("a nil Store converted to a non-nil interface (%T)", consumed)
	}
}

func TestAgentInstructionsPrefersTheStoredRow(t *testing.T) {
	store := newFakeStore()
	store.entries[testKey] = "stored guidance"
	layer := New(store, map[string]string{testKey: "yaml guidance"}, testKey)

	got, err := layer.AgentInstructions(context.Background())
	if err != nil {
		t.Fatalf("AgentInstructions() error = %v", err)
	}
	if got != "stored guidance" {
		t.Errorf("AgentInstructions() = %q, want the stored row", got)
	}
}

// A deployment whose instructions still come from YAML must read its own text,
// so the first promotion edits that document rather than replacing it with one
// section.
func TestAgentInstructionsFallsBackToTheFileValue(t *testing.T) {
	layer := New(newFakeStore(), map[string]string{testKey: "yaml guidance"}, testKey)

	got, err := layer.AgentInstructions(context.Background())
	if err != nil {
		t.Fatalf("AgentInstructions() error = %v", err)
	}
	if got != "yaml guidance" {
		t.Errorf("AgentInstructions() = %q, want the file value", got)
	}
}

// A lookup failure must NOT read as an absent row here. The read path falls
// back to the file value on a failure on purpose, but a read-modify-write that
// did so would overwrite a stored document with the file value plus a section.
func TestAgentInstructionsReturnsALookupFailure(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("db down")
	layer := New(store, map[string]string{testKey: "yaml guidance"}, testKey)

	got, err := layer.AgentInstructions(context.Background())
	if err == nil {
		t.Fatal("a lookup failure must be returned, not treated as an absent row")
	}
	if got != "" {
		t.Errorf("AgentInstructions() = %q on failure, want empty", got)
	}
}

func TestSetAgentInstructionsStoresTheValueWithItsAuthor(t *testing.T) {
	store := newFakeStore()
	layer := New(store, nil, testKey)

	if err := layer.SetAgentInstructions(context.Background(), "a rule", "reviewer@example.com"); err != nil {
		t.Fatalf("SetAgentInstructions() error = %v", err)
	}
	if len(store.written) != 1 || store.written[0] != "a rule" {
		t.Errorf("written = %v, want [a rule]", store.written)
	}
	if len(store.authors) != 1 || store.authors[0] != "reviewer@example.com" {
		t.Errorf("authors = %v, want [reviewer@example.com]", store.authors)
	}
}

// The bound is enforced at the store as well as at each writer, so no path can
// leave a value the composed instructions would carry into every session.
func TestSetAgentInstructionsRefusesAnOversizedValue(t *testing.T) {
	store := newFakeStore()
	layer := New(store, nil, testKey)

	err := layer.SetAgentInstructions(context.Background(),
		strings.Repeat("x", MaxCustomizedBytes+1), "reviewer@example.com")
	var oversize *OversizeError
	if !errors.As(err, &oversize) {
		t.Fatalf("SetAgentInstructions() error = %v, want *OversizeError", err)
	}
	if len(store.written) != 0 {
		t.Errorf("the refused value was written anyway: %v", store.written)
	}
}
