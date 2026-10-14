package platform

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/internal/platform/configwarn"
	"github.com/txn2/mcp-data-platform/internal/platform/toolkitcfg"
)

// kindDataHub is the toolkit kind a configuration upgrading from DataHub
// still declares.
const kindDataHub = "datahub"

// keySemantic is the semantic layer's section, where a configuration upgrading
// from DataHub names its instance and URN mapping.
const keySemantic = "semantic"

// DataHubImport is the DataHub a configuration upgrading from it names, read
// once by the catalog import so the deployment's curation carries into the
// platform catalog (#2124). Nothing else of DataHub runs.
type DataHubImport struct {
	// URL is DataHub's GMS address, from the toolkits.datahub instance.
	URL string
	// Token is the instance's personal access token.
	Token string
	// Platform is semantic.urn_mapping.platform: the DataHub platform name a
	// Trino table was cataloged under, when it is not "trino".
	Platform string
	// CatalogMapping is semantic.urn_mapping.catalog_mapping: each Trino
	// catalog's name in DataHub.
	CatalogMapping map[string]string
}

// dataHubSetting is one setting of the DataHub integration: where it sits,
// and the struct a strict decode reports it missing from.
type dataHubSetting struct {
	path   []string
	parent string
}

// dataHubSettings are the settings that configured DataHub. This release
// reads toolkits.datahub and semantic.urn_mapping for the import, accepts the
// rest, and acts on none of them.
var dataHubSettings = []dataHubSetting{
	{[]string{"semantic", "instance"}, "platform.SemanticConfig"},
	{[]string{"semantic", "urn_mapping"}, "platform.SemanticConfig"},
	{[]string{"semantic", "lineage"}, "platform.SemanticConfig"},
	{[]string{"query", "urn_mapping"}, "platform.QueryConfig"},
	{[]string{"storage"}, "platform.Config"},
	{[]string{"enrichment", "datahub_query_enrichment"}, "platform.EnrichmentConfig"},
	{[]string{"enrichment", "datahub_storage_enrichment"}, "platform.EnrichmentConfig"},
	{[]string{"enrichment", "s3_semantic_enrichment"}, "platform.EnrichmentConfig"},
	{[]string{"enrichment", "search_schema_preview"}, "platform.EnrichmentConfig"},
	{[]string{"enrichment", "schema_preview_max_columns"}, "platform.EnrichmentConfig"},
	{[]string{"knowledge", "apply", "datahub_connection"}, "platform.KnowledgeApplyConfig"},
}

// dataHubUpgrade is what a configuration still says about DataHub.
type dataHubUpgrade struct {
	found  []string
	source *DataHubImport
}

// readDataHubUpgrade reads the DataHub settings a configuration still carries,
// and the DataHub its toolkits.datahub instance names. YAML that does not
// parse is left to the decode that follows.
func readDataHubUpgrade(expanded []byte) dataHubUpgrade {
	var doc map[string]any
	if err := yaml.Unmarshal(expanded, &doc); err != nil {
		return dataHubUpgrade{}
	}
	var up dataHubUpgrade
	if provider, ok := lookupPath(doc, keySemantic, "provider").(string); ok && strings.EqualFold(provider, kindDataHub) {
		up.found = append(up.found, "semantic.provider: datahub")
	}
	if lookupPath(doc, "toolkits", kindDataHub) != nil {
		up.found = append(up.found, "toolkits.datahub")
	}
	for _, s := range dataHubSettings {
		if lookupPath(doc, s.path...) != nil {
			up.found = append(up.found, strings.Join(s.path, "."))
		}
	}
	slices.Sort(up.found)
	up.source = dataHubSource(doc)
	return up
}

// dataHubSource reads the DataHub the toolkits.datahub instance names: the one
// semantic.instance chose, else the kind's default, else the first by name.
// It is the DataHub the deployment used: the toolkit was enabled, or the
// semantic layer read it, which it did whether or not the toolkit was. A
// configuration with neither, or with no DataHub address, names none.
func dataHubSource(doc map[string]any) *DataHubImport {
	toolkits, _ := lookupPath(doc, "toolkits").(map[string]any)
	kind, _ := toolkits[kindDataHub].(map[string]any)
	provider, _ := lookupPath(doc, keySemantic, "provider").(string)
	if !toolkitcfg.KindEnabled(kind) && !strings.EqualFold(provider, kindDataHub) {
		return nil
	}
	instance, _ := lookupPath(doc, keySemantic, "instance").(string)
	cfg := dataHubInstance(toolkits, instance)
	address := stringSetting(cfg, "url")
	if address == "" {
		address = stringSetting(cfg, "endpoint")
	}
	if address == "" {
		return nil
	}
	src := &DataHubImport{URL: address, Token: stringSetting(cfg, "token")}
	src.Platform, _ = lookupPath(doc, keySemantic, "urn_mapping", "platform").(string)
	if m, ok := lookupPath(doc, keySemantic, "urn_mapping", "catalog_mapping").(map[string]any); ok {
		src.CatalogMapping = make(map[string]string, len(m))
		for k, v := range m {
			src.CatalogMapping[k] = fmt.Sprint(v)
		}
	}
	return src
}

// dataHubInstance is one DataHub instance's settings over those the kind's
// shared config block holds, or nil when there is no such instance.
func dataHubInstance(toolkits map[string]any, instance string) map[string]any {
	cfg := toolkitcfg.InstanceConfig(toolkits, kindDataHub, instance)
	if cfg == nil {
		return nil
	}
	shared, ok := lookupPath(toolkits, kindDataHub, "config").(map[string]any)
	if !ok {
		return cfg
	}
	merged := make(map[string]any)
	maps.Copy(merged, shared)
	maps.Copy(merged, cfg)
	return merged
}

// stringSetting reads a string setting, trimmed.
func stringSetting(cfg map[string]any, key string) string {
	s, _ := cfg[key].(string)
	return strings.TrimSpace(s)
}

// apply makes a decoded configuration the one this release runs: the DataHub
// it imports from, no DataHub toolkit, and the platform catalog as the
// semantic layer. Each setting found is logged once.
func (up dataHubUpgrade) apply(cfg *Config) {
	if len(up.found) == 0 {
		return
	}
	cfg.DataHubImport = up.source
	delete(cfg.Toolkits, kindDataHub)
	if strings.EqualFold(cfg.Semantic.Provider, kindDataHub) {
		cfg.Semantic.Provider = ""
	}
	configwarn.Warn(context.Background(), configwarn.CodeDeprecatedKey,
		"DataHub settings found: this release imports the DataHub toolkits.datahub names into the platform catalog once, "+
			"reading semantic.urn_mapping to place its datasets, and acts on no other DataHub setting; "+
			"remove them once the import has finished, since the next release refuses them",
		"settings", up.found, "import_from", importAddress(up.source))
}

// importAddress names the DataHub an import reads, or says there is none.
func importAddress(src *DataHubImport) string {
	if src == nil {
		return "none: toolkits.datahub names no url"
	}
	return src.URL
}

// accepts reports whether a strict decode's unknown-key error is about a
// DataHub setting this release accepts.
func (dataHubUpgrade) accepts(unknown string) bool {
	for _, s := range dataHubSettings {
		if strings.HasSuffix(unknown, fmt.Sprintf("field %s not found in type %s", s.path[len(s.path)-1], s.parent)) {
			return true
		}
	}
	return false
}

// lookupPath follows a path of keys through decoded YAML maps, returning nil
// where any key is absent.
func lookupPath(node any, path ...string) any {
	for _, key := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[key]
	}
	return node
}
