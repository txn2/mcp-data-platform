//go:build integration

package acceptance

import (
	"net/http"
	"testing"
)

// Issue #1985: the knowledge graph did not show scripts or assets alongside
// pages and datasets.
//
// Assets were drawn only when a page cited them and the reader could open
// them; scripts were drawn only for their owner and administrators; and
// nothing drew what a script produced. What this holds, against the running
// platform:
//
//   - a script a page cites is drawn for every reader;
//   - an asset a page cites is drawn only for a reader who can open it, and is
//     drawn for a reader once it is shared with them;
//   - a cited script's outputs are drawn as edges from the script to each file
//     the reader can open (ref_source "produced"), and the ones they cannot
//     open are counted on the script's node (hidden_outputs) and not named.
//
// The fixture is #2027's: the owner key's script produces two reports and
// shares one with the peer key; the page is promoted by the administrator.
//
// Wire forms: apply_knowledge's `page` is an object whose `references` is an
// array of strings; the graph route takes no parameters; the share route takes
// a JSON object with string members. Each is sent in that one form.

// graph2027 is the knowledge graph as one reader receives it.
type graph2027 struct {
	nodes map[string]map[string]any
	edges []map[string]any
}

func (g graph2027) hasEdge(source, target string) bool {
	for _, e := range g.edges {
		if e["source"] == source && e["target"] == target {
			return true
		}
	}
	return false
}

func (g graph2027) edge(source, target string) map[string]any {
	for _, e := range g.edges {
		if e["source"] == source && e["target"] == target {
			return e
		}
	}
	return nil
}

// graph1985 reads the knowledge graph as the reader.
func graph1985(t *testing.T, reader *client) graph2027 {
	t.Helper()
	status, body := reader.rest(http.MethodGet, "/api/v1/portal/knowledge-pages/graph", http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("GET graph: HTTP %d: %v", status, body)
	}
	g := graph2027{nodes: map[string]map[string]any{}}
	nodes, _ := body["nodes"].([]any)
	for _, n := range nodes {
		node, _ := n.(map[string]any)
		id, _ := node["id"].(string)
		g.nodes[id] = node
	}
	edges, _ := body["edges"].([]any)
	for _, e := range edges {
		edge, _ := e.(map[string]any)
		g.edges = append(g.edges, edge)
	}
	return g
}

func TestIssue1985_ACitedScriptIsDrawnForEveryReader(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)
	ref := "mcp:script:" + f.id
	pageID := promote1855(t, admin, "Churn is computed by the churn script.", []any{ref})

	for who, reader := range map[string]*client{"the owner": owner, "the peer": peer, "an administrator": admin} {
		g := graph1985(t, reader)
		node, ok := g.nodes[ref]
		if !ok {
			t.Errorf("the cited script is not drawn for %s", who)
			continue
		}
		if node["label"] != f.name || node["type"] != "script" {
			t.Errorf("the script node for %s = %v", who, node)
		}
		if !g.hasEdge("mcp:knowledge_page:"+pageID, ref) {
			t.Errorf("the page's citation of the script is not drawn for %s", who)
		}
	}
}

func TestIssue1985_ACitedAssetIsDrawnOnlyForAReaderWhoCanOpenIt(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)
	ref := "mcp:asset:" + f.privateID
	pageID := promote1855(t, admin, "The south region report is the private one.", []any{ref})
	page := "mcp:knowledge_page:" + pageID

	if g := graph1985(t, owner); !g.hasEdge(page, ref) {
		t.Errorf("the owner does not see the page's citation of their asset")
	}
	if g := graph1985(t, peer); g.hasEdge(page, ref) {
		t.Errorf("the peer sees a citation of an asset not shared with them")
	}

	status, share := owner.rest(http.MethodPost, "/api/v1/portal/assets/"+f.privateID+"/shares",
		jsonBody(t, map[string]any{"shared_with_email": devPeerEmailAddr, "permission": "viewer"}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("share: HTTP %d: %v", status, share)
	}
	if g := graph1985(t, peer); !g.hasEdge(page, ref) {
		t.Errorf("the peer does not see the citation once the asset is shared with them")
	}
}

func TestIssue1985_AScriptsOutputsAreDrawnUnderTheReadersAccess(t *testing.T) {
	admin := connect(t)
	owner := connectAs(t, devOwnerAPIKey)
	peer := connectAs(t, devPeerAPIKey)
	f := setup2027(t, owner)
	ref := "mcp:script:" + f.id
	promote1855(t, admin, "Churn is computed by the churn script.", []any{ref})

	g := graph1985(t, peer)
	edge := g.edge(ref, "mcp:asset:"+f.sharedID)
	if edge == nil {
		t.Fatalf("the output shared with the peer is not drawn from the script")
	}
	if edge["ref_source"] != "produced" || edge["type"] != "asset" {
		t.Errorf("the produced edge = %v; want ref_source produced, type asset", edge)
	}
	if g.hasEdge(ref, "mcp:asset:"+f.privateID) {
		t.Errorf("an output not shared with the peer is drawn")
	}
	if _, drawn := g.nodes["mcp:asset:"+f.privateID]; drawn {
		t.Errorf("an output not shared with the peer is a node")
	}
	if hidden := g.nodes[ref]["hidden_outputs"]; hidden != float64(1) {
		t.Errorf("hidden_outputs = %v; want 1", hidden)
	}

	og := graph1985(t, owner)
	if !og.hasEdge(ref, "mcp:asset:"+f.privateID) || !og.hasEdge(ref, "mcp:asset:"+f.sharedID) {
		t.Errorf("the owner does not see both of their outputs drawn")
	}
	if _, counted := og.nodes[ref]["hidden_outputs"]; counted {
		t.Errorf("the owner is told of outputs they cannot open: %v", og.nodes[ref])
	}
}
