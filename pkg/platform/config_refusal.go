package platform

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// catalogImportRelease is the last release line that carries the one-time
// import of a DataHub catalog into the platform catalog (#1923).
const catalogImportRelease = "v1.146"

// removedCatalogSettings are the settings that configured DataHub, each as the
// path of keys that leads to it. The platform's own catalog replaced DataHub
// (#1916), and a configuration that still names it is refused rather than
// started without the catalog its operator thinks it has.
var removedCatalogSettings = [][]string{
	{"toolkits", "datahub"},
	{"enrichment", "datahub_query_enrichment"},
	{"enrichment", "datahub_storage_enrichment"},
	{"knowledge", "apply", "datahub_connection"},
}

// refuseRemovedCatalog refuses a configuration that names DataHub: the
// semantic provider value or one of the removed settings. The message names
// every place it does so and the import that carries a DataHub catalog over.
// YAML that does not parse is left to the decode that follows.
func refuseRemovedCatalog(expanded []byte) error {
	var doc map[string]any
	if err := yaml.Unmarshal(expanded, &doc); err != nil {
		return nil //nolint:nilerr // the strict decode reports malformed YAML with its position
	}
	var found []string
	if provider, ok := lookupPath(doc, "semantic", "provider").(string); ok && strings.EqualFold(provider, "datahub") {
		found = append(found, "semantic.provider: datahub")
	}
	for _, path := range removedCatalogSettings {
		if lookupPath(doc, path...) != nil {
			found = append(found, strings.Join(path, "."))
		}
	}
	if len(found) == 0 {
		return nil
	}
	slices.Sort(found)
	return fmt.Errorf("DataHub is no longer supported; the platform's own catalog replaced it, "+
		"and this configuration still names it at %s. A deployment that runs DataHub carries its curation "+
		"into the platform catalog once, with Admin > Catalog > Import on %s, before it upgrades; "+
		"remove these settings to start this release", strings.Join(found, ", "), catalogImportRelease)
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
