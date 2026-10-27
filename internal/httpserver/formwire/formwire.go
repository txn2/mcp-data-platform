// Package formwire assembles forms on the platform's server (#2177-#2180):
// each accepted submission stored in the webhook source made for its form,
// platform.decrypt answered, the viewer key an embedded authenticated form's
// token is signed with, and the reader of a form's submissions. It is the
// composition root's, split out of internal/httpserver for its size budget.
package formwire

import (
	"context"

	trinoclient "github.com/txn2/mcp-trino/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epsubmit"
	"github.com/txn2/mcp-data-platform/internal/httpserver/webhookwire"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	trinotoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// trinoKind is the Trino toolkit's kind in the registry.
const trinoKind = "trino"

// Attach makes forms store their submissions in webhook sources and answers
// platform.decrypt (#2178, #2179), where there are endpoints and webhooks.
func Attach(p *platform.Platform, hooks *webhookwire.Webhooks) {
	if p == nil || hooks == nil {
		return
	}
	e := p.Endpoints()
	if e == nil {
		return
	}
	hooks.AttachForms(webhookwire.FormDeps{
		Runner: e.Runner, Commands: e.Commands, Tokens: e.TokenIssuer(), Audit: e.Runner.Audit,
		Sealer: p.RestEncryptor(), Connections: scratchConnections(p),
		MaxFileBytes: maxUploadBytes(p.Config().Resources.Managed.MaxUploadBytes),
	})
}

// maxUploadBytes is the most a managed resource may hold here: the
// configured limit, or resource.MaxUploadBytes when none is set.
func maxUploadBytes(configured int64) int64 {
	if configured > 0 {
		return configured
	}
	return resource.MaxUploadBytes
}

// ViewerKey is the key an embedded authenticated form's viewer token is
// signed with (#2177), the one the endpoint layer opens it with; nil where
// there are no endpoints.
func ViewerKey(p *platform.Platform) []byte {
	if p == nil {
		return nil
	}
	if e := p.Endpoints(); e != nil {
		return epform.ViewerKey(e.Runner.FormKey)
	}
	return nil
}

// Submissions reads a form's stored submissions for its owner (#2180),
// through the Trino connection its source is on; nil without a database or a
// Trino toolkit.
func Submissions(p *platform.Platform) *epsubmit.Reader {
	tt := trino(p)
	if tt == nil || p.DB() == nil {
		return nil
	}
	return &epsubmit.Reader{
		Sources: whsource.NewStore(p.DB(), p.RestEncryptor()), Tables: whtable.New(tt, p.Config().Resources.Managed.S3Bucket),
		Query: func(ctx context.Context, connection, sql string) ([]map[string]any, error) {
			res, err := tt.Query(ctx, connection, sql, trinoclient.QueryOptions{Limit: epsubmit.MaxLimit})
			if err != nil {
				return nil, err //nolint:wrapcheck // the toolkit names the query
			}
			return res.Rows, nil
		},
		Sealer: p.RestEncryptor(), Audit: p.Audit().Logger(),
	}
}

// scratchConnections names the Trino connections a form's source can be
// created on; nil without a Trino toolkit.
func scratchConnections(p *platform.Platform) func() []string {
	if tt := trino(p); tt != nil {
		return tt.ScratchConnections
	}
	return nil
}

// trino is the platform's Trino toolkit, nil without one.
func trino(p *platform.Platform) *trinotoolkit.Toolkit {
	if p == nil || p.ToolkitRegistry() == nil {
		return nil
	}
	for _, tk := range p.ToolkitRegistry().GetByKind(trinoKind) {
		if tt, ok := tk.(*trinotoolkit.Toolkit); ok {
			return tt
		}
	}
	return nil
}
