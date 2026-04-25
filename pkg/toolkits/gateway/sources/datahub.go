package sources

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// DataHubGetEntityFunc resolves a DataHub entity by URN.
type DataHubGetEntityFunc func(ctx context.Context, urn string) (any, error)

// DataHubGetGlossaryTermFunc resolves a DataHub glossary term by URN.
type DataHubGetGlossaryTermFunc func(ctx context.Context, urn string) (any, error)

// DataHubSource exposes read-only DataHub lookups for enrichment rules.
// The underlying calls are passed in as functions so this package
// doesn't depend on a specific DataHub SDK version.
type DataHubSource struct {
	getEntity       DataHubGetEntityFunc
	getGlossaryTerm DataHubGetGlossaryTermFunc
}

// NewDataHubSource builds a DataHub-backed source. Either function may be
// nil — the corresponding operation will report unsupported at execute
// time. This lets a deployment opt-in to a subset of operations without
// having to provide a stub for the rest.
func NewDataHubSource(getEntity DataHubGetEntityFunc, getGlossaryTerm DataHubGetGlossaryTermFunc) *DataHubSource {
	return &DataHubSource{getEntity: getEntity, getGlossaryTerm: getGlossaryTerm}
}

// Name returns the canonical source name "datahub".
func (*DataHubSource) Name() string { return enrichment.SourceDataHub }

// Operations returns the read-only operation allowlist.
func (*DataHubSource) Operations() []string {
	return []string{"get_entity", "get_glossary_term"}
}

// Execute dispatches the requested operation. Recognized parameters:
//
//	get_entity         { urn string }
//	get_glossary_term  { urn string }
func (s *DataHubSource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	switch op {
	case "get_entity":
		return s.execGet(ctx, op, params, s.getEntity)
	case "get_glossary_term":
		return s.execGet(ctx, op, params, s.getGlossaryTerm)
	default:
		return nil, fmt.Errorf("datahub: operation %q not supported", op)
	}
}

func (*DataHubSource) execGet(ctx context.Context, op string, params map[string]any,
	fn func(context.Context, string) (any, error),
) (any, error) {
	if fn == nil {
		return nil, fmt.Errorf("datahub: %s not configured", op)
	}
	urn, err := requireString(params, "urn")
	if err != nil {
		return nil, fmt.Errorf("datahub %s: %w", op, err)
	}
	res, err := fn(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("datahub %s: %w", op, err)
	}
	return res, nil
}
