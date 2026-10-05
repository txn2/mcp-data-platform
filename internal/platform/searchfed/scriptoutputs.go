package searchfed

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/producedview"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// scriptOutputsLimit bounds the files a fetched script lists, newest first.
const scriptOutputsLimit = 50

// scriptOutputs lists what a script produced for one fetch caller: the files
// they can open, named, and how many more there are (#2027).
type scriptOutputs struct {
	reader *producedview.Reader
	access *producedview.Access
}

// newScriptOutputs builds the lister, or nil when the deployment keeps no
// producer record.
func newScriptOutputs(cfg Config) knowledge.ScriptOutputs {
	if cfg.Producers == nil {
		return nil
	}
	var (
		assets      producedview.AssetGetter
		assetNames  producedview.AssetNames
		collections producedview.CollectionNames
		resources   producedview.ResourceNames
	)
	if cfg.AssetStore != nil {
		assets, assetNames = cfg.AssetStore, cfg.AssetStore
	}
	if cfg.CollectionStore != nil {
		collections = cfg.CollectionStore
	}
	if cfg.ResourceStore != nil {
		resources = cfg.ResourceStore
	}
	return scriptOutputs{
		reader: producedview.New(cfg.Producers, assetNames, resources, collections, nil),
		access: producedview.NewAccess(assets, collections, cfg.ShareStore, resources),
	}
}

// Outputs lists the files the script produced that the caller can open, by
// the rules fetch dereferences each by: a run reaches its author's own files
// and neither the share graph nor an administrator's reach.
func (s scriptOutputs) Outputs(ctx context.Context, scriptID string, caller knowledge.Caller) (knowledge.ScriptOutputSet, error) {
	unattended := caller.ProducerID != "" || caller.OnBehalfOf != ""
	email := caller.Email
	if caller.OnBehalfOf != "" {
		// A run's files are filed under the person it acts for.
		email = caller.OnBehalfOf
	}
	opener := s.access.For(producedview.Viewer{
		UserID: caller.UserID, Email: email, Admin: caller.IsAdmin,
		Claims: knowledge.ResourceClaimsOf(caller), Unattended: unattended,
	})
	produced, err := s.reader.ProducedFor(ctx, scriptID, scriptOutputsLimit, opener)
	if err != nil {
		return knowledge.ScriptOutputSet{}, fmt.Errorf("listing script outputs: %w", err)
	}
	out := make([]knowledge.ScriptOutput, 0, len(produced.Open))
	for _, it := range produced.Open {
		out = append(out, knowledge.ScriptOutput{
			Kind: it.TargetKind, ID: it.TargetID, Name: it.Name, Reference: outputReference(it),
		})
	}
	return knowledge.ScriptOutputSet{Open: out, Hidden: produced.Hidden, More: produced.More}, nil
}

// outputReference is the citation fetch dereferences to a produced file.
func outputReference(it producedview.Item) string {
	switch it.TargetKind {
	case producedby.TargetAsset:
		return knowledgepage.AssetRef(it.TargetID)
	case producedby.TargetCollection:
		return knowledgepage.CollectionRef(it.TargetID)
	default:
		return knowledgepage.ResourceRef(it.TargetID)
	}
}

// sharedAsset is the share graph asset fetch opens an asset by for a person
// (#2027), or nil when the deployment keeps no shares.
func sharedAsset(shares portal.ShareStore) knowledge.AssetShareLookup {
	lookup := producedview.SharedWith(shares)
	if lookup == nil {
		return nil
	}
	return lookup
}

// Compile-time checks: the stores the lister resolves through.
var (
	_ producedview.AssetGetter     = portal.AssetStore(nil)
	_ producedview.ResourceNames   = resource.Store(nil)
	_ producedview.CollectionNames = portal.CollectionStore(nil)
)
