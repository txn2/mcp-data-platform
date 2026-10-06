package portal

import (
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/producedview"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

const (
	// graphOutputsPerScript bounds the files read for one script, newest
	// first; a script that wrote more says so (more_outputs).
	graphOutputsPerScript = 50
	// refSourceProduced marks an edge from a script to a file its runs wrote,
	// as against a page's citation.
	refSourceProduced = "produced"
)

// addProduced draws, for every script the graph holds, the files its runs
// produced that the viewer can open, as edges from the script (#1985). The
// rest are counted on the script's node and not named, so the graph never
// shows a viewer the name of an asset that was not shared with them (#2027).
// A file since deleted is neither drawn nor counted.
func (b *knowledgeGraphBuilder) addProduced() {
	if len(b.scripts) == 0 || b.h.deps.Producers == nil {
		return
	}
	reader := producedview.New(b.h.deps.Producers, b.h.deps.AssetStore, b.h.deps.ResourceReader, b.h.deps.CollectionStore, nil)
	opener := producedview.NewAccess(b.h.deps.AssetStore, b.h.deps.CollectionStore, b.h.deps.ShareStore,
		b.h.deps.ResourceReader).For(producedview.Viewer{
		UserID: b.user.UserID, Email: b.user.Email,
		Admin: b.h.access.IsAdmin(b.user), Claims: b.h.resourceClaims(b.user),
	})
	for _, scriptID := range b.scripts {
		produced, err := reader.ProducedFor(b.r.Context(), scriptID, graphOutputsPerScript, opener)
		if err != nil {
			slog.Warn("knowledge graph: reading what a script produced failed",
				"script_id", logsan.SanitizeForLog(scriptID), "error", logsan.SanitizeForLog(err.Error()))
			continue
		}
		source := knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetScript, ScriptID: scriptID}.URN()
		for _, it := range produced.Open {
			b.addOutput(source, it)
		}
		b.setOutputCounts(source, produced)
	}
}

// addOutput draws one produced file and its edge from the script.
func (b *knowledgeGraphBuilder) addOutput(source string, it producedview.Item) {
	target := outputRef(it).URN()
	if target == "" {
		return
	}
	label := it.Name
	if label == "" {
		label = it.TargetID
	}
	if !b.ensureNode(target, resolvedRef{Type: it.TargetKind, Label: label, Exists: true, Accessible: true}) {
		return
	}
	b.edges = append(b.edges, knowledgeGraphEdge{
		Source: source, Target: target, Type: it.TargetKind, RefSource: refSourceProduced,
	})
}

// setOutputCounts records on a script's node how many of its outputs the
// viewer cannot open, and whether it wrote more than were read.
func (b *knowledgeGraphBuilder) setOutputCounts(id string, produced producedview.Produced) {
	for i := range b.nodes {
		if b.nodes[i].ID == id {
			b.nodes[i].HiddenOutputs = produced.Hidden
			b.nodes[i].MoreOutputs = produced.More
			return
		}
	}
}

// outputRef is the reference a produced file is drawn as.
func outputRef(it producedview.Item) knowledgepage.EntityRef {
	switch it.TargetKind {
	case producedview.TargetAsset:
		return knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetAsset, AssetID: it.TargetID}
	case producedview.TargetCollection:
		return knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetCollection, CollectionID: it.TargetID}
	default:
		return knowledgepage.EntityRef{TargetType: knowledgepage.RefTargetResource, ResourceID: it.TargetID}
	}
}
