package searchfed

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/producedview"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// promptReader reads one prompt by id, the half of the prompt store a page's
// prompt reference is judged through.
type promptReader interface {
	GetByID(ctx context.Context, id string) (*prompt.Prompt, error)
}

// pageReferenceOpener is the rule a fetched knowledge page's references are
// shown by (#2028): an asset, collection or resource by producedview.Access,
// the rule the portal and a fetched script's outputs already apply, and a
// prompt by the visibility search and fetch read it under. A kind whose store
// this deployment lacks opens for no one.
func pageReferenceOpener(cfg Config) knowledge.ReferenceOpener {
	var (
		assets      producedview.AssetGetter
		collections producedview.CollectionNames
		resources   producedview.ResourceNames
	)
	if cfg.AssetStore != nil {
		assets = cfg.AssetStore
	}
	if cfg.CollectionStore != nil {
		collections = cfg.CollectionStore
	}
	if cfg.ResourceStore != nil {
		resources = cfg.ResourceStore
	}
	files := producedview.NewAccess(assets, collections, cfg.ShareStore, resources)
	prompts, _ := cfg.PromptStore.(promptReader)
	return func(ctx context.Context, ref knowledgepage.EntityRef, caller knowledge.Caller) bool {
		if ref.TargetType == knowledgepage.RefTargetPrompt {
			return promptVisible(ctx, prompts, ref.PromptID, caller)
		}
		item, judged := fileItem(ref)
		if !judged {
			return true
		}
		return files.For(viewerOf(caller)).CanOpen(ctx, item)
	}
}

// promptVisible reports whether the caller may read the prompt.
func promptVisible(ctx context.Context, prompts promptReader, id string, caller knowledge.Caller) bool {
	if prompts == nil {
		return false
	}
	p, err := prompts.GetByID(ctx, id)
	return err == nil && p != nil && knowledge.PromptVisibleTo(p, caller)
}

// fileItem is the file a reference names, false for a reference to no file.
func fileItem(ref knowledgepage.EntityRef) (producedview.Item, bool) {
	switch ref.TargetType {
	case knowledgepage.RefTargetAsset:
		return producedview.Item{TargetKind: producedview.TargetAsset, TargetID: ref.AssetID}, true
	case knowledgepage.RefTargetCollection:
		return producedview.Item{TargetKind: producedview.TargetCollection, TargetID: ref.CollectionID}, true
	case knowledgepage.RefTargetResource:
		return producedview.Item{TargetKind: producedview.TargetResource, TargetID: ref.ResourceID}, true
	}
	return producedview.Item{}, false
}
