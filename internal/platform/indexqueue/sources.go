package indexqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	apigatewaycatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalogindex"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// ToolEnumerator enumerates the live, globally-visible tool corpus the tools
// source embeds. The platform implements it over its in-process MCP server; the
// owner depends on this narrow interface instead of a *Platform back-reference,
// so the queue is constructible and testable without a platform.
type ToolEnumerator interface {
	EnumerateGlobalTools(ctx context.Context) ([]*mcp.Tool, error)
}

// catalogSource implements indexjobs.Source for the api-catalog kind.
// LoadItems fetches the current spec content and parses it into per-operation
// embeddable items; OnSucceeded reloads live connections so their in-memory
// vector map picks up the new rows.
type catalogSource struct {
	store    apigatewaycatalog.Store
	registry *registry.Registry
}

// Kind reports the api-catalog source kind.
func (*catalogSource) Kind() string { return catalogindex.SourceKind }

// LoadItems decodes the source_id, fetches the spec content, and returns one
// item per operation. A missing spec surfaces as an error (the worker treats it
// as terminal: the spec was deleted).
func (s *catalogSource) LoadItems(ctx context.Context, sourceID string) ([]indexjobs.Item, error) {
	catalogID, specName, ok := catalogindex.DecodeSourceID(sourceID)
	if !ok {
		return nil, fmt.Errorf("catalogSource: malformed source_id %q", sourceID)
	}
	spec, err := s.store.GetSpec(ctx, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogSource: get spec: %w", err)
	}
	ops, err := apigatewaykit.BuildOperationItems(spec.Content, specName)
	if err != nil {
		return nil, fmt.Errorf("catalogSource: build items: %w", err)
	}
	items := make([]indexjobs.Item, len(ops))
	for i, op := range ops {
		items[i] = indexjobs.Item{ItemID: op.OperationID, Text: op.Text}
	}
	return items, nil
}

// OnSucceeded asks every registered api-gateway toolkit to rebuild connections
// that mount the catalog so their in-memory vector map picks up the
// freshly-written rows.
func (s *catalogSource) OnSucceeded(sourceID string) {
	if s.registry == nil {
		return
	}
	catalogID, _, ok := catalogindex.DecodeSourceID(sourceID)
	if !ok {
		return
	}
	for _, tk := range s.registry.All() {
		if api, ok := tk.(*apigatewaykit.Toolkit); ok {
			api.ReloadConnectionsByCatalog(catalogID)
		}
	}
}

// toolsSource is the indexjobs.Source for the tools kind. Unlike the api-catalog
// Source, which reads spec rows from a DB table, the tool corpus is the
// in-process registry: LoadItems enumerates the live, globally-visible tools
// through the injected ToolEnumerator. The worker runs in the same process as
// the registry, and the resulting vectors persist to a shared table, so every
// replica and restart reads the same set.
type toolsSource struct {
	enum              ToolEnumerator
	discoveryToolName string
}

// Kind reports the tools source kind.
func (*toolsSource) Kind() string { return toolsindex.SourceKind }

// LoadItems returns one item per globally-visible tool (excluding the discovery
// tool itself). The sourceID is ignored: there is a single tool corpus per
// deployment.
func (s *toolsSource) LoadItems(ctx context.Context, _ string) ([]indexjobs.Item, error) {
	if s.enum == nil {
		return nil, errors.New("toolsSource: no tool enumerator")
	}
	tools, err := s.enum.EnumerateGlobalTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("toolsSource: %w", err)
	}
	items := make([]indexjobs.Item, 0, len(tools))
	for _, t := range tools {
		if t.Name == s.discoveryToolName {
			continue
		}
		items = append(items, indexjobs.Item{ItemID: t.Name, Text: toolEmbedText(t)})
	}
	return items, nil
}

// OnSucceeded is a no-op: platform_find_tools reads vectors from the shared
// table at query time, so there is no in-process cache to refresh after a
// successful embed.
func (*toolsSource) OnSucceeded(_ string) {}

// toolEmbedText builds the text embedded for a tool: its name, description, and
// a summary of its top-level parameters. The description carries most of the
// semantic signal; the parameter summary adds the vocabulary of what the tool
// operates on.
func toolEmbedText(t *mcp.Tool) string {
	text := t.Name
	if t.Description != "" {
		text += "\n" + t.Description
	}
	if params := toolParamSummary(t.InputSchema); params != "" {
		text += "\nParameters: " + params
	}
	return text
}

// toolParamSummary extracts a stable, comma-separated summary of a tool's
// top-level input parameters (name and, when present, description) from its JSON
// schema. A JSON round-trip keeps this agnostic to the schema's concrete Go
// type; an unparseable or property-less schema yields an empty summary.
func toolParamSummary(schema any) string {
	if schema == nil {
		return ""
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return ""
	}
	var parsed struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	names := make([]string, 0, len(parsed.Properties))
	for name, prop := range parsed.Properties {
		if prop.Description != "" {
			names = append(names, name+" ("+prop.Description+")")
		} else {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
