package platform

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// storedConnectionsStore is a ConnectionStore holding a fixed set of rows,
// persistent so the inventory adapter reads it.
type storedConnectionsStore struct{ rows []ConnectionInstance }

func (s storedConnectionsStore) List(context.Context) ([]ConnectionInstance, error) {
	return s.rows, nil
}

func (storedConnectionsStore) Get(context.Context, string, string) (*ConnectionInstance, error) {
	return nil, ErrConnectionNotFound
}

func (storedConnectionsStore) Set(context.Context, ConnectionInstance) error { return nil }

func (storedConnectionsStore) Delete(context.Context, string, string) error { return nil }

func (storedConnectionsStore) Persistent() bool { return true }

// TestStoredConnections_ReadsTheRowsThatSayWhatExists covers the projection the
// platform supplies: a row's kind, name and description, and the catalog its
// configuration names, which is what the enumeration reports for a connection
// no replica has been asked about yet.
func TestStoredConnections_ReadsTheRowsThatSayWhatExists(t *testing.T) {
	p := &Platform{
		toolkitRegistry: registry.NewRegistry(),
		connectionStore: storedConnectionsStore{rows: []ConnectionInstance{
			{
				Kind: "api", Name: "billing", Description: "Billing API",
				Config: map[string]any{"base_url": "https://example.test", "catalog_id": "cat-1"},
				//nolint:exhaustruct // the remaining fields do not reach the inventory
				UpdatedAt: time.Now(),
			},
			{Kind: "trino", Name: "warehouse", Description: "The warehouse"},
		}},
	}

	inventory := StoredConnections(p)
	if inventory == nil {
		t.Fatal("a persistent store is an inventory to read")
	}
	got, err := inventory.ListStoredConnections(context.Background())
	if err != nil {
		t.Fatalf("listing the inventory: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("the inventory holds %d connections, want the store's 2: %v", len(got), got)
	}
	if got[0].Kind != "api" || got[0].Name != "billing" || got[0].Description != "Billing API" {
		t.Errorf("the api row reads %+v", got[0])
	}
	if got[0].CatalogID != "cat-1" {
		t.Errorf("the catalog the configuration names was not read: %q", got[0].CatalogID)
	}
	if got[1].Name != "warehouse" || got[1].CatalogID != "" {
		t.Errorf("the trino row reads %+v", got[1])
	}
}

// TestStoredConnections_NoneWhereConnectionsAreTheFilesAlone: a deployment whose
// store does not outlive the process holds nothing another replica wrote, so
// what this process was handed is the whole inventory.
func TestStoredConnections_NoneWhereConnectionsAreTheFilesAlone(t *testing.T) {
	if got := StoredConnections(&Platform{connectionStore: &NoopConnectionStore{}}); got != nil {
		t.Errorf("a non-persistent store is not an inventory: %v", got)
	}
	if got := StoredConnections(&Platform{}); got != nil {
		t.Errorf("no store is not an inventory: %v", got)
	}
}
