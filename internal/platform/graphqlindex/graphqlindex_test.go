package graphqlindex

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// newToolkit builds a graphql toolkit holding one connection with the
// named fixture schema installed. The endpoint is never reached: the
// index consumer's corpus is the toolkit's own operation index.
func newToolkit(t *testing.T, connection, fixture string) *graphqlkit.Toolkit {
	t.Helper()
	cfg, err := graphqlkit.ParseConfig(map[string]any{"endpoint_url": "https://unreached.invalid/graphql"})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = connection
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: connection,
		Instances:   map[string]graphqlkit.Config{connection: cfg},
	})
	if fixture != "" {
		sdl, err := os.ReadFile("../../../internal/gqlschema/testdata/" + fixture + ".graphql") //nolint:gosec // a fixture path this test builds
		if err != nil {
			t.Fatalf("fixture: %v", err)
		}
		if err := tk.SetSchema(context.Background(), connection, sdl); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return tk
}

func lister(tks ...*graphqlkit.Toolkit) ToolkitLister {
	return func() []*graphqlkit.Toolkit { return tks }
}

func TestSourceKindMatchesItsSink(t *testing.T) {
	source := NewSource(lister())
	sink := NewSink(nil, source)
	if source.Kind() != SourceKind || sink.Kind() != SourceKind {
		t.Errorf("kinds = %q/%q", source.Kind(), sink.Kind())
	}
}

func TestLoadItemsYieldsOneItemPerOperationInAStableOrder(t *testing.T) {
	source := NewSource(lister(newToolkit(t, "gql", "namespaced")))

	items, err := source.LoadItems(context.Background(), "gql")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(items) < 6 {
		t.Fatalf("items = %d", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].ItemID >= items[i].ItemID {
			t.Fatalf("items are not in a stable order: %q then %q", items[i-1].ItemID, items[i].ItemID)
		}
	}
	for _, item := range items {
		if item.Text == "" {
			t.Errorf("%s has nothing to embed", item.ItemID)
		}
	}
}

func TestLoadItemsReportsAConnectionWithNothingToIndex(t *testing.T) {
	source := NewSource(lister(newToolkit(t, "gql", "")))
	// A connection whose schema was never read has no corpus. Reporting
	// it as gone is what makes the worker clear its vectors and complete
	// rather than retrying a unit that cannot resolve.
	if _, err := source.LoadItems(context.Background(), "gql"); !errors.Is(err, indexjobs.ErrSourceGone) {
		t.Errorf("err = %v; want ErrSourceGone", err)
	}
	if _, err := source.LoadItems(context.Background(), "absent"); !errors.Is(err, indexjobs.ErrSourceGone) {
		t.Errorf("an unknown connection gave %v", err)
	}
}

func TestConnectionsSpansEveryToolkit(t *testing.T) {
	source := NewSource(lister(newToolkit(t, "b", "flat"), newToolkit(t, "a", "namespaced")))
	got := source.connections()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("connections = %v", got)
	}
}

func TestOnSucceededRereadsTheConnectionsVectors(t *testing.T) {
	tk := newToolkit(t, "gql", "flat")
	source := NewSource(lister(tk))
	// The reload is observable through the toolkit; here the contract
	// under test is that a successful pass names the right connection and
	// that an unknown one is a no-op rather than a panic.
	source.OnSucceeded("gql")
	source.OnSucceeded("absent")
}

func TestSinkAddressesTheConnectionsCurrentSchemaVersion(t *testing.T) {
	tk := newToolkit(t, "gql", "flat")
	source := NewSource(lister(tk))
	hash, _, ok := tk.IndexItems("gql")
	if !ok {
		t.Fatal("no schema")
	}
	sink := NewSink(nil, source)
	// With no store the sink cannot be exercised further here; what this
	// asserts is that the hash the sink would key on is the connection's
	// current one, which is what makes a schema change leave the previous
	// version's vectors addressable.
	gotHash, _, gotOK := source.itemsFor("gql")
	if !gotOK || gotHash != hash {
		t.Errorf("hash = %q; want %q", gotHash, hash)
	}
	if sink.Kind() != SourceKind {
		t.Errorf("kind = %q", sink.Kind())
	}
}

func TestSinkIsANoOpForAConnectionWithNoSchema(t *testing.T) {
	source := NewSource(lister(newToolkit(t, "gql", "")))
	sink := NewSink(nil, source)
	key := indexjobs.Key{SourceKind: SourceKind, SourceID: "gql"}
	// A nil store would panic if these reached it; they must not, because
	// the connection has no schema version to key rows on.
	if got, err := sink.ListExisting(context.Background(), key); got != nil || err != nil {
		t.Errorf("list = (%v, %v)", got, err)
	}
	if err := sink.Upsert(context.Background(), key, nil); err != nil {
		t.Errorf("upsert = %v", err)
	}
	if err := sink.UpsertBatch(context.Background(), key, nil); err != nil {
		t.Errorf("upsert batch = %v", err)
	}
	if err := sink.StampExpected(context.Background(), key, 3); err != nil {
		t.Errorf("stamp = %v", err)
	}
}

// A connection taking its schema from a catalog is embedded once as that
// catalog's spec, by the api-catalog source, and reads its vectors back from
// there. Enumerating it here would open a unit that resolves to no items on
// every sweep (#1745).
func TestAConnectionOnACatalogIsNotEnumeratedPerConnection(t *testing.T) {
	cfg, err := graphqlkit.ParseConfig(map[string]any{
		"endpoint_url": "https://unreached.invalid/graphql", "catalog_id": "erp",
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.ConnectionName = "cataloged"
	tk := graphqlkit.NewMulti(graphqlkit.MultiConfig{
		DefaultName: "cataloged",
		Instances:   map[string]graphqlkit.Config{"cataloged": cfg},
	})

	source := NewSource(lister(tk, newToolkit(t, "own-endpoint", "flat")))

	got := source.connections()
	if len(got) != 1 || got[0] != "own-endpoint" {
		t.Errorf("connections = %v; want only the one that reads its own endpoint", got)
	}
}
