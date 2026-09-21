package notifywiring

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// testDB is a pool the wiring can hold without any statement running against
// it: Wire opens no connection, it only builds stores over the handle.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// registeredTools asks the server what it serves, over a real in-memory
// session: a tool registered but not listed is exactly the defect worth
// catching here (#1675).
func registeredTools(t *testing.T, server *mcp.Server) map[string]bool {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

func newServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
}

func TestWire_RegistersTheTool(t *testing.T) {
	server := newServer()
	Wire(Deps{DB: testDB(t), Server: server, Enabled: true, DigestHourUTC: 13})
	if !registeredTools(t, server)["notify"] {
		t.Error("notify was not registered on a database deployment with notifications on")
	}
}

func TestWire_RegistersNothingWhenTheFeatureCannotExist(t *testing.T) {
	// Each of these is a deployment that cannot send at all. A tool listing
	// destinations it cannot write to would advertise a capability the
	// deployment does not have.
	tests := []struct {
		name string
		deps func(*mcp.Server) Deps
	}{
		{"no database", func(s *mcp.Server) Deps {
			return Deps{Server: s, Enabled: true}
		}},
		{"notifications turned off", func(s *mcp.Server) Deps {
			return Deps{DB: testDB(t), Server: s, Enabled: false}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newServer()
			Wire(tc.deps(server))
			if registeredTools(t, server)["notify"] {
				t.Error("notify was registered where nothing can be sent")
			}
		})
	}
}

func TestWire_NoServerIsSafe(t *testing.T) {
	Wire(Deps{DB: testDB(t), Enabled: true}) // must not panic
}

func TestAssetReaderOf_AbsentWithoutBothStores(t *testing.T) {
	// publish refuses rather than guessing; send is unaffected either way.
	if assetReaderOf(Deps{}) != nil {
		t.Error("a reader was built with no stores")
	}
	if assetReaderOf(Deps{Assets: stubAssetStore{}}) != nil {
		t.Error("a reader was built with no blob store")
	}
	if assetReaderOf(Deps{Blobs: stubBlobStore{}}) != nil {
		t.Error("a reader was built with no asset store")
	}
	if assetReaderOf(Deps{Assets: stubAssetStore{}, Blobs: stubBlobStore{}}) == nil {
		t.Error("no reader was built with both stores present")
	}
}

func TestAssetAccessOf_AbsentWithoutTheShareStore(t *testing.T) {
	// A deployment that cannot check entitlement must not publish on the
	// strength of an id.
	if assetAccessOf(Deps{Assets: stubAssetStore{}}) != nil {
		t.Error("an entitlement check was built with no share store")
	}
	if assetAccessOf(Deps{Assets: stubAssetStore{}, Shares: stubShareStore{}}) == nil {
		t.Error("no entitlement check was built with both stores present")
	}
}

func TestAssetAccess_RefusesANilAsset(t *testing.T) {
	a := assetAccess{}
	if a.CanRead(context.Background(), nil, "u1", "a@example.com") {
		t.Error("a nil asset was reported readable")
	}
}

func TestAssetReader_ReadsTheStoredBytes(t *testing.T) {
	asset := &portaldomain.Asset{ID: "a1", S3Bucket: "b", S3Key: "k"}
	r := assetReader{
		assets: stubAssetStore{asset: asset},
		blobs:  stubBlobStore{data: []byte("# report")},
	}
	got, err := r.Get(context.Background(), "a1")
	if err != nil || got.ID != "a1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	content, err := r.Content(context.Background(), asset)
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if string(content) != "# report" {
		t.Errorf("Content = %q", content)
	}
}

func TestAssetReader_ReportsAFailedRead(t *testing.T) {
	r := assetReader{
		assets: stubAssetStore{err: errors.New("gone")},
		blobs:  stubBlobStore{err: errors.New("bucket unreachable")},
	}
	if _, err := r.Get(context.Background(), "a1"); err == nil {
		t.Error("a failed asset read was reported as success")
	}
	if _, err := r.Content(context.Background(), &portaldomain.Asset{}); err == nil {
		t.Error("a failed content read was reported as success")
	}
}

// The two store stubs below satisfy the portal contracts by embedding the
// interface: only the methods the wiring calls are implemented, and any other
// call panics rather than silently answering zero.
type stubAssetStore struct {
	portal.AssetStore
	asset *portaldomain.Asset
	err   error
}

func (s stubAssetStore) Get(context.Context, string) (*portaldomain.Asset, error) {
	return s.asset, s.err
}

type stubBlobStore struct {
	portal.S3Client
	data []byte
	err  error
}

func (s stubBlobStore) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	return s.data, "text/markdown", s.err
}

type stubShareStore struct{ portal.ShareStore }
