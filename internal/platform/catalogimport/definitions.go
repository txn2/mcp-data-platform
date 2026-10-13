package catalogimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// liveQuery lists the entities of one type that are not marked removed.
const liveQuery = `query importLive($types: [EntityType!], $start: Int!, $count: Int!) {
  searchAcrossEntities(input: { types: $types, query: "*", start: $start, count: $count }) {
    total
    searchResults { entity { urn } }
  }
}`

// definitionKind pairs a DataHub entity type with the catalog kind it is
// imported as and the GraphQL field that reads one.
type definitionKind struct {
	entityType string
	kind       catalog.Kind
	field      string
	// parents reads the glossary node above a term or node.
	parents bool
	// owned reads ownership and documentation links, which a tag lacks.
	owned bool
}

// definitionKinds are read in this order: a glossary node before the terms
// and nodes under it.
var definitionKinds = []definitionKind{
	{entityType: "GLOSSARY_NODE", kind: catalog.KindGlossaryNode, field: "glossaryNode", parents: true, owned: true},
	{entityType: "GLOSSARY_TERM", kind: catalog.KindGlossaryTerm, field: "glossaryTerm", parents: true, owned: true},
	{entityType: "TAG", kind: catalog.KindTag, field: "tag"},
	{entityType: "DOMAIN", kind: catalog.KindDomain, field: "domain", owned: true},
	{entityType: "DATA_PRODUCT", kind: catalog.KindDataProduct, field: "dataProduct", owned: true},
}

// definitionQuery is the query that reads one definition of a kind.
func definitionQuery(k definitionKind) string {
	fields := []string{"urn properties { name description }"}
	if k.parents {
		fields = append(fields, "parentNodes { nodes { urn } }")
	}
	if k.owned {
		fields = append(fields,
			"ownership { owners { owner { ... on CorpUser { urn username } ... on CorpGroup { urn name } } type ownershipType { urn info { name } } } }",
			"institutionalMemory { elements { url description } }")
	}
	return "query importDefinition($urn: String!) { entity: " + k.field + "(urn: $urn) { " + strings.Join(fields, " ") + " } }"
}

type definitionResponse struct {
	Entity *struct {
		Properties *struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"properties"`
		ParentNodes *struct {
			Nodes []struct {
				URN string `json:"urn"`
			} `json:"nodes"`
		} `json:"parentNodes"`
		Ownership           *ownershipResponse `json:"ownership"`
		InstitutionalMemory *linksResponse     `json:"institutionalMemory"`
	} `json:"entity"`
}

// Definitions reads every glossary node, glossary term, tag, domain and data
// product that is not marked removed, nodes first.
func (d *DataHub) Definitions(ctx context.Context) ([]Definition, error) {
	out := make([]Definition, 0)
	for _, k := range definitionKinds {
		urns, err := d.list(ctx, liveQuery, k.entityType)
		if err != nil {
			return nil, err
		}
		for _, urn := range urns {
			def, err := d.definition(ctx, k, urn)
			if err != nil {
				return nil, err
			}
			out = append(out, def)
		}
	}
	return out, nil
}

// definition reads one definition.
func (d *DataHub) definition(ctx context.Context, k definitionKind, urn string) (Definition, error) {
	var resp definitionResponse
	if err := d.gql.Execute(ctx, definitionQuery(k), map[string]any{"urn": urn}, &resp); err != nil {
		return Definition{}, fmt.Errorf("reading %s: %w", urn, err)
	}
	def := Definition{URN: urn, Kind: k.kind, Name: urnKey(urn)}
	e := resp.Entity
	if e == nil {
		return def, nil
	}
	if e.Properties != nil {
		if strings.TrimSpace(e.Properties.Name) != "" {
			def.Name = e.Properties.Name
		}
		def.Aspects.Description = e.Properties.Description
	}
	if e.ParentNodes != nil && len(e.ParentNodes.Nodes) > 0 {
		def.Parent = e.ParentNodes.Nodes[0].URN
	}
	def.Aspects.Owners = owners(e.Ownership)
	def.Aspects.DocumentationLinks = links(e.InstitutionalMemory)
	return def, nil
}

// urnKey is the part of a URN after its type: the name of a tag, the id of
// a glossary term.
func urnKey(urn string) string {
	rest := strings.TrimPrefix(urn, "urn:li:")
	if _, key, ok := strings.Cut(rest, ":"); ok {
		return key
	}
	return rest
}
