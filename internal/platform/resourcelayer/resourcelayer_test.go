package resourcelayer

import (
	"context"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourceindex"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

func TestNewNilDB(t *testing.T) {
	h, err := New(nil, Config{})
	if err != nil {
		t.Fatalf("New(nil db) error = %v, want nil", err)
	}
	if h != nil {
		t.Fatalf("New(nil db) handle = %v, want nil", h)
	}
	// A nil handle must be safe for every accessor and mutation.
	if h.Store() != nil {
		t.Error("nil handle Store() should be nil")
	}
	if h.S3Client() != nil {
		t.Error("nil handle S3Client() should be nil")
	}
	if h.URIScheme() != "" {
		t.Error("nil handle URIScheme() should be empty")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	h.Register(server, &resource.Resource{URI: "mcp://x"}) // no panic
	h.Unregister(server, "mcp://x")                        // no panic
	h.LoadAll(server)                                      // no panic
}

func TestNewWithDB(t *testing.T) {
	t.Run("store-only when no S3 connection resolves", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		h, err := New(db, Config{URIScheme: "res"})
		if err != nil {
			t.Fatalf("New = %v, want nil error", err)
		}
		if h == nil || h.Store() == nil {
			t.Fatal("expected a store-backed handle")
		}
		if h.S3Client() != nil {
			t.Error("S3Client should be nil when no connection resolves")
		}
		if h.URIScheme() != "res" {
			t.Errorf("URIScheme() = %q, want res", h.URIScheme())
		}
	})

	t.Run("builds the S3 client when a connection resolves", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		h, err := New(db, Config{
			S3Bucket: "resources",
			Toolkits: map[string]any{
				"s3": map[string]any{
					"instances": map[string]any{
						"acme": map[string]any{
							"endpoint":      "http://localhost:9000",
							"access_key_id": "key",
							"secret_key":    "secret",
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("New = %v, want nil error", err)
		}
		if h == nil || h.S3Client() == nil {
			t.Fatal("expected an S3-backed handle")
		}
	})

	t.Run("errors when the referenced S3 connection is missing", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		_, err = New(db, Config{S3Connection: "ghost"})
		if err == nil {
			t.Fatal("expected an error for a missing S3 connection")
		}
	})
}

func TestAccessors(t *testing.T) {
	h := &Handle{
		store:     &countingStore{},
		uriScheme: "mcp",
	}
	if h.Store() == nil {
		t.Error("Store() should be non-nil")
	}
	if h.S3Client() != nil {
		t.Error("S3Client() should be nil when unset")
	}
	if h.URIScheme() != "mcp" {
		t.Errorf("URIScheme() = %q, want mcp", h.URIScheme())
	}
}

func TestS3Connection(t *testing.T) {
	t.Run("explicit connection returned", func(t *testing.T) {
		got := s3Connection(Config{S3Connection: "my-s3"})
		if got != "my-s3" {
			t.Errorf("got %q, want my-s3", got)
		}
	})

	t.Run("falls back to default S3 instance", func(t *testing.T) {
		got := s3Connection(Config{Toolkits: map[string]any{
			"s3": map[string]any{
				"instances": map[string]any{
					"acme": map[string]any{
						"endpoint":      "http://localhost:9000",
						"access_key_id": "key",
						"secret_key":    "secret",
					},
				},
			},
		}})
		if got != "acme" {
			t.Errorf("got %q, want acme", got)
		}
	})

	t.Run("empty when no S3 toolkit", func(t *testing.T) {
		if got := s3Connection(Config{}); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestResolveDefaultS3Instance(t *testing.T) {
	t.Run("returns instance name", func(t *testing.T) {
		got := resolveDefaultS3Instance(map[string]any{
			"s3": map[string]any{
				"instances": map[string]any{
					"mys3": map[string]any{"endpoint": "http://localhost:9000"},
				},
			},
		})
		if got != "mys3" {
			t.Errorf("got %q, want mys3", got)
		}
	})

	t.Run("returns configured default", func(t *testing.T) {
		got := resolveDefaultS3Instance(map[string]any{
			"s3": map[string]any{
				"default": "second",
				"instances": map[string]any{
					"first":  map[string]any{"endpoint": "http://a:9000"},
					"second": map[string]any{"endpoint": "http://b:9000"},
				},
			},
		})
		if got != "second" {
			t.Errorf("got %q, want second", got)
		}
	})

	t.Run("empty when no S3 toolkit", func(t *testing.T) {
		if got := resolveDefaultS3Instance(map[string]any{}); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("empty when instances not a map", func(t *testing.T) {
		got := resolveDefaultS3Instance(map[string]any{
			"s3": map[string]any{"instances": "invalid"},
		})
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("empty when kind config not a map", func(t *testing.T) {
		got := resolveDefaultS3Instance(map[string]any{"s3": "invalid"})
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestURIScheme(t *testing.T) {
	if got := uriScheme(Config{URIScheme: "custom"}); got != "custom" {
		t.Errorf("got %q, want custom", got)
	}
	if got := uriScheme(Config{}); got != resource.DefaultURIScheme {
		t.Errorf("got %q, want default %q", got, resource.DefaultURIScheme)
	}
}

// newTestHandle builds a store-only handle without a database, so registration
// behavior can be exercised in isolation. The MCP server is passed per call.
func newTestHandle(store resource.Store) *Handle {
	return &Handle{store: store, uriScheme: resource.DefaultURIScheme}
}

func TestRegisterUnregister(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	h := newTestHandle(&countingStore{})

	t.Run("nil resource is a no-op", func(_ *testing.T) {
		h.Register(server, nil) // no panic
	})

	t.Run("registers and unregisters with the server", func(_ *testing.T) {
		h.Register(server, &resource.Resource{
			URI:         "mcp://global/test/file.txt",
			DisplayName: "Test File",
			Description: "A test resource",
			MIMEType:    "text/plain",
		})
		h.Unregister(server, "mcp://global/test/file.txt")
	})

	t.Run("nil server is a no-op", func(_ *testing.T) {
		h.Register(nil, &resource.Resource{URI: "mcp://x"})
		h.Unregister(nil, "mcp://x")
	})
}

func TestLoadAll(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "1"}, nil)

	t.Run("no store is a no-op", func(_ *testing.T) {
		(&Handle{}).LoadAll(server)
	})

	t.Run("nil server is a no-op", func(_ *testing.T) {
		newTestHandle(&countingStore{}).LoadAll(nil)
	})

	t.Run("registers each global resource from the store", func(t *testing.T) {
		store := &countingStore{list: []resource.Resource{
			{URI: "mcp://global/a.txt", DisplayName: "A"},
			{URI: "mcp://global/b.txt", DisplayName: "B"},
		}}
		newTestHandle(store).LoadAll(server)
		if store.listCalls != 1 {
			t.Errorf("List called %d times, want 1", store.listCalls)
		}
	})
}

// countingStore is a resource.Store that records List calls and returns a fixed
// slice, enough to exercise LoadAll without a database.
type countingStore struct {
	list      []resource.Resource
	listCalls int
}

func (s *countingStore) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	s.listCalls++
	return s.list, len(s.list), nil
}
func (*countingStore) Insert(_ context.Context, _ resource.Resource) error { return nil }
func (*countingStore) Get(_ context.Context, _ string) (*resource.Resource, error) {
	return &resource.Resource{}, nil
}

func (*countingStore) GetByIDs(_ context.Context, _ []string) (map[string]*resource.Resource, error) {
	return map[string]*resource.Resource{}, nil
}

func (*countingStore) GetByURI(_ context.Context, _ string) (*resource.Resource, error) {
	return &resource.Resource{}, nil
}
func (*countingStore) Update(_ context.Context, _ string, _ resource.Update) error { return nil }
func (*countingStore) Delete(_ context.Context, _ string) error                    { return nil }

// fakeNotifier records Notify calls. Satisfies ListChangedNotifier.
type fakeNotifier struct{ calls int }

func (f *fakeNotifier) Notify() { f.calls++ }

// TestNotifyListChanged proves the bound notifier fires and that a nil handle or
// an unbound notifier is a safe no-op — the runtime create/delete paths call
// this; a no-database (nil handle) deployment must not panic.
func TestNotifyListChanged(t *testing.T) {
	// Nil handle: no-op.
	var nilH *Handle
	nilH.SetListChangedNotifier(&fakeNotifier{}) // no panic
	nilH.NotifyListChanged()                     // no panic

	// Bound notifier fires.
	h := &Handle{}
	f := &fakeNotifier{}
	h.SetListChangedNotifier(f)
	h.NotifyListChanged()
	h.NotifyListChanged()
	if f.calls != 2 {
		t.Errorf("notifier fired %d times, want 2", f.calls)
	}

	// Unbound notifier: no-op, no panic.
	h2 := &Handle{}
	h2.NotifyListChanged()
}

var _ ListChangedNotifier = (*fakeNotifier)(nil)

// stubReadRecorder stands in for the audit-backed recorder.
type stubReadRecorder struct{}

func (stubReadRecorder) RecordRead(_ context.Context, _ resource.ReadEvent) {}

func TestReadRecorderBinding(t *testing.T) {
	t.Run("nil handle is safe", func(t *testing.T) {
		var h *Handle
		h.SetReadRecorder(stubReadRecorder{}) // no panic
		if h.ReadRecorder() != nil {
			t.Error("nil handle must expose no recorder")
		}
	})

	t.Run("unbound handle exposes no recorder", func(t *testing.T) {
		h := &Handle{}
		if h.ReadRecorder() != nil {
			t.Error("an unbound handle must expose no recorder; audit-disabled deployments rely on it")
		}
	})

	t.Run("bound recorder is returned", func(t *testing.T) {
		h := &Handle{}
		rec := stubReadRecorder{}
		h.SetReadRecorder(rec)
		if h.ReadRecorder() != resource.ReadRecorder(rec) {
			t.Error("ReadRecorder did not return the bound recorder")
		}
	})
}

func TestStoreCapabilities(t *testing.T) {
	t.Run("nil handle has neither capability", func(t *testing.T) {
		var h *Handle
		if h.VersionStore() != nil || h.ReadTracker() != nil {
			t.Error("a nil handle must expose no store capabilities")
		}
	})

	t.Run("a store without the capabilities yields nil", func(t *testing.T) {
		h := &Handle{store: &countingStore{}}
		if h.VersionStore() != nil {
			t.Error("a metadata-only store must not be reported as versioning-capable")
		}
		if h.ReadTracker() != nil {
			t.Error("a metadata-only store must not be reported as read-tracking")
		}
	})

	t.Run("the postgres store carries both", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		h, err := New(db, Config{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if h.VersionStore() == nil {
			t.Error("the postgres store implements VersionStore; the revision routes depend on this assertion")
		}
		if h.ReadTracker() == nil {
			t.Error("the postgres store implements ReadTracker; last-read sorting depends on this assertion")
		}
	})
}

// TestIndexProducer covers the write-path index-job seam the queue binds
// (#1256): the resource store carries a producer for the resources kind, and a
// nil handle (no database) exposes nothing to bind.
func TestIndexProducer(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, err := New(db, Config{})
	if err != nil {
		t.Fatalf("New = %v, want nil error", err)
	}
	if got := h.IndexProducer().Kind(); got != resourceindex.SourceKind {
		t.Errorf("IndexProducer().Kind() = %q, want %q", got, resourceindex.SourceKind)
	}

	var nilHandle *Handle
	if nilHandle.IndexProducer() != nil {
		t.Error("nil handle IndexProducer() should be nil")
	}
}
