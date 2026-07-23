package portalstore

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// fakeS3Client satisfies portal.S3Client and records whether Close was called
// (and returns a configurable error) so the shutdown seam can be asserted.
type fakeS3Client struct {
	closed   bool
	closeErr error
}

func (*fakeS3Client) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}

func (*fakeS3Client) PutObjectStream(_ context.Context, _, _ string, _ io.Reader, _ string) (int64, error) {
	return 0, nil
}

func (*fakeS3Client) GetObject(_ context.Context, _, _ string) (data []byte, contentType string, err error) { //nolint:gocritic // named for clarity
	return nil, "", nil
}
func (*fakeS3Client) DeleteObject(_ context.Context, _, _ string) error { return nil }

func (f *fakeS3Client) Close() error {
	f.closed = true
	return f.closeErr
}

func TestNew_NilDBReturnsNil(t *testing.T) {
	t.Parallel()
	if h := New(nil, nil, nil, Config{}); h != nil {
		t.Fatalf("New(nil db) = %v, want nil (no-op without a database)", h)
	}
}

func TestNew_S3BackedWiresEveryStoreAndToolkit(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s3 := &fakeS3Client{}
	h := New(db, s3, nil, Config{Name: "default", S3Bucket: "b", S3Prefix: "p", BaseURL: "https://x"})
	if h == nil {
		t.Fatal("New with a db must return a non-nil handle")
	}
	if h.AssetStore() == nil {
		t.Error("AssetStore() = nil, want the postgres asset store")
	}
	if h.ShareStore() == nil {
		t.Error("ShareStore() = nil, want the postgres share store")
	}
	if h.VersionStore() == nil {
		t.Error("VersionStore() = nil, want the postgres version store")
	}
	if h.CollectionStore() == nil {
		t.Error("CollectionStore() = nil, want the postgres collection store")
	}
	if h.ThreadStore() == nil {
		t.Error("ThreadStore() = nil, want the postgres thread store")
	}
	if h.KnowledgePageStore() == nil {
		t.Error("KnowledgePageStore() = nil, want the postgres knowledge-page store")
	}
	if h.S3Client() != portal.S3Client(s3) {
		t.Error("S3Client() did not return the injected S3 client")
	}
	if h.Toolkit() == nil {
		t.Error("Toolkit() = nil, want the assembled asset toolkit for registration")
	}
	if got := h.Toolkit().Kind(); got != "portal" {
		t.Errorf("Toolkit().Kind() = %q, want %q", got, "portal")
	}
}

func TestNew_DatabaseOnlyLeavesS3Nil(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// No S3 client (database-only mode): the stores must still be wired and the
	// toolkit assembled, but S3Client() reports nil.
	h := New(db, nil, nil, Config{Name: "default"})
	if h == nil {
		t.Fatal("New with a db must return a non-nil handle in database-only mode")
	}
	if h.AssetStore() == nil {
		t.Error("AssetStore() = nil in database-only mode, want the postgres store")
	}
	if h.S3Client() != nil {
		t.Error("S3Client() = non-nil in database-only mode, want nil")
	}
	if h.Toolkit() == nil {
		t.Error("Toolkit() = nil in database-only mode, want the assembled toolkit")
	}
}

func TestNewFromStores_AccessorsReturnInjectedStores(t *testing.T) {
	t.Parallel()
	asset := portal.NewNoopAssetStore()
	share := portal.NewNoopShareStore()
	version := portal.NewNoopVersionStore()
	collection := portal.NewNoopCollectionStore()
	// portal has no noop thread store; the postgres constructor just wraps the
	// (here nil) *sql.DB and gives a distinct value to assert the accessor
	// returns exactly what was injected. It is never queried in this test.
	thread := portal.NewPostgresThreadStore(nil)
	s3 := &fakeS3Client{}

	h := NewFromStores(Stores{
		Asset:      asset,
		Share:      share,
		Version:    version,
		Collection: collection,
		Thread:     thread,
		S3Client:   s3,
	}, nil, Config{Name: "default"})

	if h.AssetStore() != asset {
		t.Error("AssetStore() did not return the injected asset store")
	}
	if h.ShareStore() != share {
		t.Error("ShareStore() did not return the injected share store")
	}
	if h.VersionStore() != version {
		t.Error("VersionStore() did not return the injected version store")
	}
	if h.CollectionStore() != collection {
		t.Error("CollectionStore() did not return the injected collection store")
	}
	if h.ThreadStore() != thread {
		t.Error("ThreadStore() did not return the injected thread store")
	}
	if h.S3Client() != portal.S3Client(s3) {
		t.Error("S3Client() did not return the injected S3 client")
	}
	if h.Toolkit() == nil {
		t.Error("Toolkit() = nil, want the assembled toolkit")
	}
}

func TestClose_ClosesS3Client(t *testing.T) {
	t.Parallel()
	s3 := &fakeS3Client{}
	h := NewFromStores(Stores{S3Client: s3}, nil, Config{})
	if err := h.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if !s3.closed {
		t.Error("Close() did not close the S3 client")
	}
}

func TestClose_PropagatesS3Error(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	s3 := &fakeS3Client{closeErr: wantErr}
	h := NewFromStores(Stores{S3Client: s3}, nil, Config{})
	if err := h.Close(); !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want it to wrap %v", err, wantErr)
	}
}

func TestClose_DatabaseOnlyIsNoOp(t *testing.T) {
	t.Parallel()
	// No S3 client: Close has nothing to close and must not error.
	h := NewFromStores(Stores{}, nil, Config{})
	if err := h.Close(); err != nil {
		t.Errorf("Close() with no S3 client = %v, want nil", err)
	}
}

func TestNilHandle_AccessorsAndCloseAreSafe(t *testing.T) {
	t.Parallel()
	var h *Handle
	if h.AssetStore() != nil {
		t.Error("nil Handle AssetStore() != nil")
	}
	if h.ShareStore() != nil {
		t.Error("nil Handle ShareStore() != nil")
	}
	if h.VersionStore() != nil {
		t.Error("nil Handle VersionStore() != nil")
	}
	if h.CollectionStore() != nil {
		t.Error("nil Handle CollectionStore() != nil")
	}
	if h.ThreadStore() != nil {
		t.Error("nil Handle ThreadStore() != nil")
	}
	if h.KnowledgePageStore() != nil {
		t.Error("nil Handle KnowledgePageStore() != nil")
	}
	if h.S3Client() != nil {
		t.Error("nil Handle S3Client() != nil")
	}
	if h.Toolkit() != nil {
		t.Error("nil Handle Toolkit() != nil")
	}
	if err := h.Close(); err != nil {
		t.Errorf("nil Handle Close() = %v, want nil", err)
	}
}

// TestSaveToolNameIsRegistered guards the provenance-harvest wiring: Platform
// configures MCPProvenanceMiddleware with SaveToolName, and harvest is silent
// when that string does not match a tool the toolkit actually registers. The
// assertion runs against a real tools/list over an in-memory session rather
// than against Tools(), which returns the same constant and so could not fail.
func TestSaveToolNameIsRegistered(t *testing.T) {
	t.Parallel()
	h := NewFromStores(Stores{}, nil, Config{Name: "portal"})

	server := mcp.NewServer(&mcp.Implementation{Name: "portalstore-test", Version: "0.0.1"}, nil)
	h.Toolkit().RegisterTools(server)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0.0.1"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	advertised := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		advertised = append(advertised, tool.Name)
	}
	if !slices.Contains(advertised, SaveToolName) {
		t.Errorf("SaveToolName %q is not advertised by tools/list (%v); provenance harvest would silently stop",
			SaveToolName, advertised)
	}
}
