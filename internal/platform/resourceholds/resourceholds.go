// Package resourceholds answers what still points at a managed resource
// (#1665).
//
// A managed resource is referenced from two places that outlive it: an asset's
// content declares it, and a prompt attaches it as reference material. Neither
// is a foreign key -- deleting the file leaves both rows behind, by design, so
// the thing that depended on it reports the material as missing rather than
// quietly losing the evidence that it ever had any.
//
// A knowledge page is deliberately NOT a third: ParseCitableRef refuses an
// mcp:resource: citation on a shared page, because a resource is
// visibility-scoped and the citation would be broken for every reader outside
// that scope. Every writer of knowledge_page_entity_refs -- the manual picker,
// the body scan and promotion -- goes through that check, so no page can hold a
// resource row and a lookup for one would be a count that is always zero.
//
// That design is what makes this package necessary. Nothing in the database
// stops a delete, so the count of what would break has to be gathered and put
// to whoever is deleting, before the delete rather than after it.
//
// The answer is counts and not names. Every one of these records carries an
// audience of its own -- an asset has shares, a page has readers -- and the
// person deleting a file is not necessarily in any of them. The portal's
// used-by panel already resolves those audiences and names what the reader may
// open; this is the part that can be said to anybody who can see the file.
package resourceholds

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

// Holds is what still points at one managed resource, counted by kind.
//
// It is this package's own type rather than the one the asset toolkit publishes
// so that nothing here depends on a toolkit: the three reverse lookups are the
// whole of what this package is, and the surface that puts the counts to
// somebody is free to be any surface. The composition root adapts it to the
// toolkit's shape where the two are wired together.
type Holds struct {
	Assets  int
	Prompts int
	// More says a count was cut at the bound rather than being the whole of
	// what points at the file.
	More bool
}

// maxCounted bounds each of the two reads. A caller deciding whether to
// delete acts the same way on "12 assets" as on "50 or more", so the answer
// says which of those it is rather than paying for an unbounded count.
const maxCounted = 50

// Refs is the asset-reference store as this package reads it: which assets
// declare a target in their content.
type Refs interface {
	ListByTarget(ctx context.Context, kind assetrefs.TargetKind, targetID string, limit int) ([]assetrefs.Ref, error)
}

// Attachments is the prompt-attachment store as this package reads it: which
// prompts attach a resource as reference material.
type Attachments interface {
	ListByResource(ctx context.Context, resourceID string) ([]string, error)
}

// Deps are the three reverse lookups a checker is assembled from. Each is
// optional: a deployment without one of these layers has nothing of that kind
// pointing at anything, which is a count of zero rather than a failure.
type Deps struct {
	Refs        Refs
	Attachments Attachments
}

// Checker gathers what points at a managed resource.
type Checker struct {
	refs        Refs
	attachments Attachments
}

// New builds the checker. Every dependency being absent still yields one: it
// answers zero of everything, which is the true answer for a deployment that
// has no asset or prompt layer to point at a file with.
func New(d Deps) *Checker {
	return &Checker{refs: d.Refs, attachments: d.Attachments}
}

// ResourceHolds counts the records still pointing at a resource.
//
// A failed read is returned rather than absorbed. Every other degraded answer
// on this path is still a visible one; here an empty count would read as
// "nothing depends on this file", which is the single wrong answer somebody
// about to delete it must not act on.
func (c *Checker) ResourceHolds(ctx context.Context, resourceID string) (Holds, error) {
	var out Holds
	assets, err := c.countAssets(ctx, resourceID)
	if err != nil {
		return out, err
	}
	prompts, err := c.countPrompts(ctx, resourceID)
	if err != nil {
		return out, err
	}
	out.Assets, out.Prompts = assets, prompts
	out.More = assets >= maxCounted || prompts >= maxCounted
	return out, nil
}

// countAssets counts the assets whose content references the file.
func (c *Checker) countAssets(ctx context.Context, resourceID string) (int, error) {
	if c.refs == nil {
		return 0, nil
	}
	rows, err := c.refs.ListByTarget(ctx, assetrefs.TargetResource, resourceID, maxCounted)
	if err != nil {
		return 0, fmt.Errorf("reading the assets referencing this file: %w", err)
	}
	return len(rows), nil
}

// countPrompts counts the prompts attaching the file as reference material.
func (c *Checker) countPrompts(ctx context.Context, resourceID string) (int, error) {
	if c.attachments == nil {
		return 0, nil
	}
	rows, err := c.attachments.ListByResource(ctx, resourceID)
	if err != nil {
		return 0, fmt.Errorf("reading the prompts attaching this file: %w", err)
	}
	return min(len(rows), maxCounted), nil
}
