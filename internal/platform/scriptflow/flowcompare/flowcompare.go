// Package flowcompare compares two versions of a script's flow graph (#1908):
// the newer version's diagram marked with what it reads, writes and produces
// that the older did not, which is what a reviewer approving a change asks
// first.
package flowcompare

import (
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptflow"
)

// removedPrefix names a node or edge carried over from the older version.
const removedPrefix = "was:"

// Compare returns newer as a diagram of what changed since older: each of
// newer's nodes marked added or changed (with what it said before), and each
// node only older had carried over, marked removed, with its edges.
//
// Nodes are matched by what they are and what they reach, never by line: the
// same kind reaching the same connection, tool, destination, table or target
// is the same step wherever it moved to, so reordering functions changes
// nothing. A node whose name or purpose matches but whose reach differs is
// changed, which is how a destination moving from the portal to a bucket reads
// as one change naming both. A computed value compares as the source text it
// is written as, so it is never read as a change to or from a guessed name.
func Compare(older, newer scriptflow.Graph, olderVersion int) scriptflow.Graph {
	out := newer
	out.ComparedWith = olderVersion
	out.Nodes = slices.Clone(newer.Nodes)
	pending := make([]int, 0, len(older.Nodes))
	for i := range older.Nodes {
		pending = append(pending, i)
	}
	matched := map[string]string{} // older id -> newer id
	p := pairing{older: older, nodes: out.Nodes, pending: &pending, matched: matched}
	unmatched := p.by(signature, "")
	unmatched = p.by(identity, scriptflow.ChangeChanged, unmatched...)
	for _, i := range unmatched {
		out.Nodes[i].Change = scriptflow.ChangeAdded
	}
	removed := map[string]bool{}
	for _, i := range pending {
		n := older.Nodes[i]
		n.ID, n.Change = removedPrefix+n.ID, scriptflow.ChangeRemoved
		out.Nodes = append(out.Nodes, n)
		removed[n.ID] = true
		matched[older.Nodes[i].ID] = n.ID
	}
	out.Edges = append(slices.Clone(newer.Edges), removedEdges(older.Edges, matched, removed)...)
	return out
}

// pairing is one comparison's matching state: the older graph, the newer
// nodes being marked, the older node indexes not yet matched, and each matched
// older id's newer id.
type pairing struct {
	older   scriptflow.Graph
	nodes   []scriptflow.Node
	pending *[]int
	matched map[string]string
}

// by matches the newer nodes at indexes (every node when none are given)
// against the older nodes still pending, by key, marking a match with change
// and recording what the older node said when the match is a change. It
// returns the indexes of the newer nodes left unmatched.
func (p pairing) by(key func(scriptflow.Node) string, change string, indexes ...int) []int {
	older, nodes, pending, matched := p.older, p.nodes, p.pending, p.matched
	if indexes == nil {
		indexes = make([]int, 0, len(nodes))
		for i := range nodes {
			indexes = append(indexes, i)
		}
	}
	left := make([]int, 0, len(indexes))
	for _, i := range indexes {
		j := slices.IndexFunc(*pending, func(p int) bool { return key(older.Nodes[p]) == key(nodes[i]) })
		if j < 0 {
			left = append(left, i)
			continue
		}
		old := older.Nodes[(*pending)[j]]
		*pending = slices.Delete(*pending, j, j+1)
		matched[old.ID] = nodes[i].ID
		if change != "" {
			nodes[i].Change = change
			nodes[i].Was = &scriptflow.Was{Title: old.Title, Subtitle: old.Subtitle, Purpose: old.Purpose, Detail: old.Detail}
		}
	}
	return left
}

// removedEdges is the older version's edges that touch a removed node, with
// each end renamed to the node it became in the compared graph.
func removedEdges(older []scriptflow.Edge, matched map[string]string, removed map[string]bool) []scriptflow.Edge {
	out := []scriptflow.Edge{}
	for _, e := range older {
		from, okFrom := matched[e.From]
		to, okTo := matched[e.To]
		if !okFrom || !okTo || (!removed[from] && !removed[to]) {
			continue
		}
		e.From, e.To, e.Change = from, to, scriptflow.ChangeRemoved
		out = append(out, e)
	}
	return out
}

// signature is everything a step says it reaches: two steps with the same
// signature are the same step, wherever the source put them.
func signature(n scriptflow.Node) string {
	return strings.Join([]string{n.Kind, n.Title, n.Subtitle, n.Purpose, strings.Join(n.Detail, "\n")}, keySep)
}

// keySep joins the parts of a matching key; no part can contain it.
const keySep = "\x00"

// identity is what names a step apart from where it goes: the output an export
// writes, the table a query reads, the path an API call takes, the operation a
// tool performs. Two steps with one identity and different signatures are one
// step that changed.
func identity(n scriptflow.Node) string {
	switch n.Kind {
	case scriptflow.KindQuery:
		return n.Kind + keySep + strings.Join(n.Detail, "\n")
	case scriptflow.KindState, scriptflow.KindSaveState, scriptflow.KindResult:
		return n.Kind
	case scriptflow.KindAPI, scriptflow.KindTool, scriptflow.KindWrite:
		return n.Kind + keySep + n.Subtitle + keySep + n.Purpose
	}
	return n.Kind + keySep + n.Subtitle
}
