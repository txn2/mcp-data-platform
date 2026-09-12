package graphql

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The connection lifecycle after #1676: a configuration change is an update
// that keeps the stored schema, a deletion is what drops it, and the store is
// what every instance answers from when its own read fails.

// namespacedOperationCount is how many operations the namespaced fixture
// exposes, asserted exactly so a survived schema is proved to be the whole
// schema rather than any schema.
func namespacedOperationCount(t *testing.T) int {
	t.Helper()
	tk := newToolkit(t, newUpstream(t), "namespaced", nil)
	info, _ := tk.SchemaInfo("gql")
	if info.OperationCount == 0 {
		t.Fatal("the namespaced fixture exposes no operations")
	}
	return info.OperationCount
}

func TestUpdateConnectionRereadsTheEndpointWithTheNewConfig(t *testing.T) {
	u := newUpstream(t)
	u.introspection = flatIntrospectionResult
	tk := newToolkit(t, u, "", nil)

	err := tk.UpdateConnection("gql", map[string]any{
		"endpoint_url": u.server.URL, "static_headers": map[string]any{"X-Tenant": "acme"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	info, _ := tk.SchemaInfo("gql")
	if info.Source != SchemaSourceIntrospection || info.OperationCount == 0 || info.Error != "" {
		t.Errorf("info = %+v; an update re-reads the endpoint", info)
	}
	if got := u.lastHeaders().Get("X-Tenant"); got != "acme" {
		t.Errorf("the re-read went out without the new configuration: X-Tenant = %q", got)
	}
}

func TestUpdateConnectionKeepsTheStoredSchemaWhenTheRereadFails(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	tk := newToolkit(t, u, "", nil)
	store := newMemorySchemaStore()
	tk.SetSchemaStore(store)
	if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	before, _ := tk.SchemaInfo("gql")

	err := tk.UpdateConnection("gql", map[string]any{"endpoint_url": u.server.URL, "read_only": true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	after, _ := tk.SchemaInfo("gql")
	if after.Hash != before.Hash || after.Source != SchemaSourceUpload || after.OperationCount != namespacedOperationCount(t) {
		t.Errorf("the upload did not survive the save: before %+v, after %+v", before, after)
	}
	if !strings.Contains(after.Error, "introspection is not allowed") {
		t.Errorf("the failed re-read is not reported beside the schema: %+v", after)
	}
	if _, err := store.GetSchema(context.Background(), "gql"); err != nil {
		t.Errorf("the save dropped the stored schema: %v", err)
	}
	// The new configuration is live.
	refusal := refuseQuery(t, tk, QueryInput{Connection: "gql", Query: `mutation { sales { salesOrder { delete(_id: "1") } } }`})
	if !strings.Contains(refusal, "read_only") {
		t.Errorf("refusal = %q; the saved read_only did not take effect", refusal)
	}
}

func TestUpdateConnectionWithoutAStoreKeepsWhatTheConnectionHeld(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	if err := tk.UpdateConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("update: %v", err)
	}

	after, _ := tk.SchemaInfo("gql")
	if after.Hash != before.Hash || after.OperationCount != before.OperationCount {
		t.Errorf("a deployment with no store lost the schema on a save: before %+v, after %+v", before, after)
	}
	if after.Error == "" {
		t.Error("the failed re-read is not reported")
	}
}

func TestUpdateConnectionRefusesABadConfigAndLeavesTheConnectionAlone(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	if err := tk.UpdateConnection("gql", map[string]any{}); err == nil {
		t.Error("a configuration with no endpoint was accepted")
	}
	if err := tk.UpdateConnection("gql", map[string]any{"endpoint_url": u.server.URL, "auth_mode": "nonsense"}); err == nil {
		t.Error("a configuration with no authenticator was accepted")
	}
	if err := tk.UpdateConnection("absent", map[string]any{"endpoint_url": u.server.URL}); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("updating an unknown connection gave %v", err)
	}

	after, _ := tk.SchemaInfo("gql")
	if after != before {
		t.Errorf("a refused update changed the connection: before %+v, after %+v", before, after)
	}
}

func TestAddConnectionLoadsTheStoreWhenTheReadFails(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	first := newToolkit(t, u, "", nil)
	first.SetSchemaStore(store)
	if err := first.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// A second instance over the same store: the replica that did not
	// serve the upload, receiving the connection through the reload bus.
	second := NewMulti(MultiConfig{DefaultName: "other"})
	second.SetSchemaStore(store)
	if err := second.AddConnection("gql", map[string]any{"endpoint_url": u.server.URL}); err != nil {
		t.Fatalf("add: %v", err)
	}

	info, _ := second.SchemaInfo("gql")
	if info.Source != SchemaSourceUpload || info.OperationCount != namespacedOperationCount(t) {
		t.Errorf("info = %+v; the store is what a replica whose own read fails serves", info)
	}
	// The registration's read was about no schema, not about the upload, so
	// this replica answers what the replica that served the upload answers:
	// the upload with nothing beside it (#1703).
	onFirst, _ := first.SchemaInfo("gql")
	if info.Error != onFirst.Error {
		t.Errorf("info = %+v; the replica that served the upload reports %q", info, onFirst.Error)
	}
	if stored, _ := store.GetSchema(context.Background(), "gql"); stored.ReadError != "" {
		t.Errorf("a registration read marked the upload refused: %+v", stored)
	}
}

func TestLoadStoredSchemaReplacesWhatTheInstanceHolds(t *testing.T) {
	u := newUpstream(t)
	store := newMemorySchemaStore()
	first := newToolkit(t, u, "", nil)
	first.SetSchemaStore(store)
	second := newToolkit(t, u, "", nil)
	second.SetSchemaStore(store)
	if err := second.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}

	if err := first.SetSchema(context.Background(), "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := second.LoadStoredSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("load: %v", err)
	}

	onFirst, _ := first.SchemaInfo("gql")
	onSecond, _ := second.SchemaInfo("gql")
	if onSecond.Hash != onFirst.Hash || onSecond.Source != SchemaSourceUpload || onSecond.OperationCount != onFirst.OperationCount {
		t.Errorf("the two instances hold different schemas: %+v vs %+v", onFirst, onSecond)
	}
	if onSecond.Error != "" {
		t.Errorf("a schema the store answered with is not a failure: %+v", onSecond)
	}
	if onSecond.FetchedAt.IsZero() || !onSecond.FetchedAt.Equal(onFirst.FetchedAt) {
		t.Errorf("fetched_at differs across instances: %v vs %v", onFirst.FetchedAt, onSecond.FetchedAt)
	}
}

func TestLoadStoredSchemaWithNothingToLoad(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	// No store: nothing to load, nothing changed.
	if err := tk.LoadStoredSchema(context.Background(), "gql"); err != nil {
		t.Errorf("an instance with no store: %v", err)
	}
	// A store with no row for the connection.
	tk.SetSchemaStore(newMemorySchemaStore())
	if err := tk.LoadStoredSchema(context.Background(), "gql"); !errors.Is(err, ErrSchemaNotFound) {
		t.Errorf("an empty store gave %v; want ErrSchemaNotFound", err)
	}
	// A store that cannot answer.
	down := newMemorySchemaStore()
	down.getErr = errors.New("database is down")
	tk.SetSchemaStore(down)
	if err := tk.LoadStoredSchema(context.Background(), "gql"); err == nil {
		t.Error("a store that cannot answer was reported as loaded")
	}
	// A stored schema that does not parse.
	broken := newMemorySchemaStore()
	broken.schemas["gql"] = StoredSchema{Connection: "gql", SDL: "type Query {", Source: SchemaSourceUpload}
	tk.SetSchemaStore(broken)
	if err := tk.LoadStoredSchema(context.Background(), "gql"); err == nil {
		t.Error("a stored schema that does not parse was reported as loaded")
	}
	if err := tk.LoadStoredSchema(context.Background(), "absent"); !errors.Is(err, ErrConnectionNotFound) {
		t.Errorf("loading an unknown connection gave %v", err)
	}

	after, _ := tk.SchemaInfo("gql")
	if after != before {
		t.Errorf("a load with nothing to install changed the connection: before %+v, after %+v", before, after)
	}
}

func TestSetSchemaDoesNotRecordAMalformedUploadOnTheConnection(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	if err := tk.SetSchema(context.Background(), "gql", []byte("type Query {")); err == nil {
		t.Fatal("malformed SDL was accepted")
	}

	after, _ := tk.SchemaInfo("gql")
	if after != before {
		t.Errorf("the operator's bad input was recorded on the connection: before %+v, after %+v", before, after)
	}
}

func TestARefreshThatFailsKeepsTheSchemaAndReportsBesideIt(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	tk := newToolkit(t, u, "flat", nil)
	before, _ := tk.SchemaInfo("gql")

	if err := tk.RefreshSchema(context.Background(), "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}

	after, _ := tk.SchemaInfo("gql")
	if after.Hash != before.Hash || after.OperationCount != before.OperationCount || after.Source != before.Source {
		t.Errorf("a failed refresh changed the schema: before %+v, after %+v", before, after)
	}
	if !strings.Contains(after.Error, "introspection is not allowed") {
		t.Errorf("the failure is not reported: %+v", after)
	}
	// The next refresh that succeeds clears it.
	u.introspection = flatIntrospectionResult
	if err := tk.RefreshSchema(context.Background(), "gql"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if cleared, _ := tk.SchemaInfo("gql"); cleared.Error != "" {
		t.Errorf("a refresh that succeeded left the old failure in place: %+v", cleared)
	}
}

func TestARefusedRefreshIsRecordedInTheStoreAndReachesAnotherInstance(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	first := newToolkit(t, u, "", nil)
	first.SetSchemaStore(store)
	second := newToolkit(t, u, "", nil)
	second.SetSchemaStore(store)
	ctx := context.Background()
	if err := first.SetSchema(ctx, "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := second.LoadStoredSchema(ctx, "gql"); err != nil {
		t.Fatalf("load: %v", err)
	}

	if err := first.RefreshSchema(ctx, "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}
	stored, _ := store.GetSchema(ctx, "gql")
	if !strings.Contains(stored.ReadError, "introspection is not allowed") {
		t.Errorf("the refusal was not recorded with the stored schema: %+v", stored)
	}
	if err := second.LoadStoredSchema(ctx, "gql"); err != nil {
		t.Fatalf("load: %v", err)
	}
	onFirst, _ := first.SchemaInfo("gql")
	onSecond, _ := second.SchemaInfo("gql")
	if onSecond.Error != onFirst.Error || onSecond.Hash != onFirst.Hash {
		t.Errorf("the instance that did not run the read answers differently: %+v vs %+v", onFirst, onSecond)
	}

	// An upload installs a schema, and a schema installed has no refusal
	// beside it anywhere.
	if err := first.SetSchema(ctx, "gql", fixtureSDL(t, "namespaced")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err := second.LoadStoredSchema(ctx, "gql"); err != nil {
		t.Fatalf("load: %v", err)
	}
	if cleared, _ := second.SchemaInfo("gql"); cleared.Error != "" {
		t.Errorf("an upload left the refusal in place: %+v", cleared)
	}
}

func TestARefusedRefreshTheStoreCannotRecordIsStillReportedHere(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	tk := newToolkit(t, u, "", nil)
	tk.SetSchemaStore(store)
	ctx := context.Background()
	if err := tk.SetSchema(ctx, "gql", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("upload: %v", err)
	}
	store.recordErr = errors.New("database is down")

	if err := tk.RefreshSchema(ctx, "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}
	if info, _ := tk.SchemaInfo("gql"); !strings.Contains(info.Error, "introspection is not allowed") {
		t.Errorf("a store that could not record the refusal cost this instance its report: %+v", info)
	}
}

// TestARefusalAboutAnOlderVersionIsNotRecordedBesideANewerOne is the race the
// two-replica acceptance run found: a replica's read of the endpoint began
// before it held the schema another replica had just uploaded, and finished
// after. The refusal is about the version the reader held, so the upload in
// the store is left with nothing beside it, and the reader, falling back to
// the store, reports the upload as every other replica does.
func TestARefusalAboutAnOlderVersionIsNotRecordedBesideANewerOne(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	uploader := newToolkit(t, u, "", nil)
	uploader.SetSchemaStore(store)
	reader := newToolkit(t, u, "", nil)
	reader.SetSchemaStore(store)
	ctx := context.Background()

	// The reader holds no schema when its read begins; the upload lands
	// while the read is in flight.
	u.onIntrospection = func() {
		if err := uploader.SetSchema(ctx, "gql", fixtureSDL(t, "namespaced")); err != nil {
			t.Errorf("upload: %v", err)
		}
	}
	reader.readOrLoadStored(ctx, "gql")

	stored, _ := store.GetSchema(ctx, "gql")
	if stored.Hash == "" || stored.ReadError != "" {
		t.Errorf("a read that began before the upload marked it refused: %+v", stored)
	}
	onReader, _ := reader.SchemaInfo("gql")
	onUploader, _ := uploader.SchemaInfo("gql")
	if onReader.Hash != onUploader.Hash || onReader.Error != onUploader.Error {
		t.Errorf("the two instances answer differently: uploader %+v, reader %+v", onUploader, onReader)
	}
}

// TestARefusalAboutAVersionReplacedDuringTheReadIsNotReportedBesideTheNewOne:
// the same race on the instance that ran the read. A peer's upload reaches it
// through the reload bus while its re-read is in flight; the refusal is about
// the version it held before, so it reports the newer one as the store and
// every other replica do.
func TestARefusalAboutAVersionReplacedDuringTheReadIsNotReportedBesideTheNewOne(t *testing.T) {
	u := newUpstream(t) // refuses introspection
	store := newMemorySchemaStore()
	peer := newToolkit(t, u, "", nil)
	peer.SetSchemaStore(store)
	reader := newToolkit(t, u, "", nil)
	reader.SetSchemaStore(store)
	ctx := context.Background()
	if err := reader.SetSchema(ctx, "gql", fixtureSDL(t, "flat")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	u.onIntrospection = func() {
		if err := peer.SetSchema(ctx, "gql", fixtureSDL(t, "namespaced")); err != nil {
			t.Errorf("peer upload: %v", err)
		}
		if err := reader.LoadStoredSchema(ctx, "gql"); err != nil {
			t.Errorf("announcement: %v", err)
		}
	}
	if err := reader.RefreshSchema(ctx, "gql"); err == nil {
		t.Fatal("the fake endpoint answered the introspection query")
	}

	onReader, _ := reader.SchemaInfo("gql")
	onPeer, _ := peer.SchemaInfo("gql")
	if onReader.Hash != onPeer.Hash || onReader.Error != "" || onPeer.Error != "" {
		t.Errorf("the refusal was reported beside the version that replaced it: reader %+v, peer %+v", onReader, onPeer)
	}
	if stored, _ := store.GetSchema(ctx, "gql"); stored.ReadError != "" {
		t.Errorf("the store marked the newer version refused: %+v", stored)
	}
}
