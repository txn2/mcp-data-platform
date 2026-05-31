package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// platformFindToolsName is the discovery tool's own name. It is
// excluded from the index so a find-tools query never ranks the
// discovery tool itself.
const platformFindToolsName = "platform_find_tools"

// enumerateGlobalTools returns the globally-visible tool descriptors by
// running tools/list over an unauthenticated in-memory session. With no
// caller identity the visibility middleware resolves no roles, so it
// applies only the global allow/deny patterns and skips persona
// filtering (see pkg/middleware/mcp_visibility.go filterTools): the
// result is the persona-neutral corpus to embed once. Descriptions are
// post-override (the description middleware runs in the same chain).
func (p *Platform) enumerateGlobalTools(ctx context.Context) ([]*mcp.Tool, error) {
	if p.mcpServer == nil {
		return nil, errors.New("tools index: mcp server not initialized")
	}
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := p.mcpServer.Connect(ctx, t1, nil)
	if err != nil {
		return nil, fmt.Errorf("tools index: server connect: %w", err)
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "tools-index-internal", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("tools index: client connect: %w", err)
	}
	defer func() { _ = cs.Close() }()

	var out []*mcp.Tool
	params := &mcp.ListToolsParams{}
	for {
		res, err := cs.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("tools index: list tools: %w", err)
		}
		out = append(out, res.Tools...)
		if res.NextCursor == "" {
			break
		}
		params.Cursor = res.NextCursor
	}
	return out, nil
}

// toolEmbedText builds the text embedded for a tool: its name,
// description, and a summary of its top-level parameters. The
// description carries most of the semantic signal; the parameter
// summary adds the vocabulary of what the tool operates on.
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

// toolParamSummary extracts a stable, comma-separated summary of a
// tool's top-level input parameters (name and, when present,
// description) from its JSON schema. A JSON round-trip keeps this
// agnostic to the schema's concrete Go type; an unparseable or
// property-less schema yields an empty summary.
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

// toolsSource is the indexjobs.Source for the tools kind. Unlike the
// api-catalog Source, which reads spec rows from a DB table, the tool
// corpus is the in-process registry: LoadItems enumerates the live,
// globally-visible tools. The worker runs in the same process as the
// registry, and the resulting vectors persist to a shared table, so
// every replica and restart reads the same set.
type toolsSource struct {
	p *Platform
}

// Kind reports the tools source kind.
func (*toolsSource) Kind() string { return toolsindex.SourceKind }

// LoadItems returns one item per globally-visible tool (excluding the
// discovery tool itself). The sourceID is ignored: there is a single
// tool corpus per deployment.
func (s *toolsSource) LoadItems(ctx context.Context, _ string) ([]indexjobs.Item, error) {
	tools, err := s.p.enumerateGlobalTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("toolsSource: %w", err)
	}
	items := make([]indexjobs.Item, 0, len(tools))
	for _, t := range tools {
		if t.Name == platformFindToolsName {
			continue
		}
		items = append(items, indexjobs.Item{ItemID: t.Name, Text: toolEmbedText(t)})
	}
	return items, nil
}

// OnSucceeded is a no-op: platform_find_tools reads vectors from the
// shared table at query time, so there is no in-process cache to
// refresh after a successful embed.
func (*toolsSource) OnSucceeded(_ string) {}
