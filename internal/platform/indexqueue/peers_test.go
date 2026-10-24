package indexqueue

import (
	"context"
	"testing"

	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// recordingPeers records what a replica announced on the reload bus.
type recordingPeers struct {
	catalogs    []string
	connections [][3]string
}

func (r *recordingPeers) PublishCatalogReload(_ context.Context, catalogID string) {
	r.catalogs = append(r.catalogs, catalogID)
}

func (r *recordingPeers) PublishConnectionReload(_ context.Context, kind, name, op string) {
	r.connections = append(r.connections, [3]string{kind, name, op})
}

// TestPeersAnnounceWhatAnEmbedWrote: a finished api-catalog embed is
// announced as a catalog reload and a finished graphql embed as that
// connection's vectors op, and with no bus nothing is announced (#2276).
func TestPeersAnnounceWhatAnEmbedWrote(t *testing.T) {
	if (Config{}).catalogPeers() != nil || (Config{}).graphqlPeers() != nil {
		t.Fatal("a config with no bus announced")
	}
	peers := &recordingPeers{}
	cfg := Config{Peers: peers}
	cfg.catalogPeers()("cat")
	cfg.graphqlPeers()("gql")
	if len(peers.catalogs) != 1 || peers.catalogs[0] != "cat" {
		t.Errorf("catalog announcements %v; want cat once", peers.catalogs)
	}
	want := [3]string{graphqlkit.Kind, "gql", graphqlkit.VectorsReloadOp}
	if len(peers.connections) != 1 || peers.connections[0] != want {
		t.Errorf("connection announcements %v; want %v once", peers.connections, want)
	}
}
