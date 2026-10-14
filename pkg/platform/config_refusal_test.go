package platform

import (
	"strings"
	"testing"
)

// A configuration that still names the removed catalog integration is refused
// at load, naming every place it does so and the release whose importer
// carries the catalog over, rather than started without the catalog its
// operator thinks it has.
func TestLoadConfigRefusesTheRemovedCatalogIntegration(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want []string
	}{
		{"semantic provider", "semantic:\n  provider: DataHub\n", []string{"semantic.provider: datahub"}},
		{"toolkit", "toolkits:\n  datahub:\n    primary: {}\n", []string{"toolkits.datahub"}},
		{
			"enrichment and apply", "enrichment:\n  datahub_query_enrichment: true\n  datahub_storage_enrichment: false\n" +
				"knowledge:\n  apply:\n    datahub_connection: primary\n",
			[]string{"enrichment.datahub_query_enrichment", "enrichment.datahub_storage_enrichment", "knowledge.apply.datahub_connection"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfigFromBytes([]byte(tc.yaml))
			if err == nil {
				t.Fatal("the configuration loaded")
			}
			for _, want := range append(tc.want, catalogImportRelease, "Admin > Catalog > Import") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

func TestLoadConfigAcceptsTheCatalogsOwnSettings(t *testing.T) {
	for _, yaml := range []string{"semantic:\n  provider: noop\n", "semantic: {}\n", "server:\n  name: x\n"} {
		if err := refuseRemovedCatalog([]byte(yaml)); err != nil {
			t.Errorf("%q refused: %v", yaml, err)
		}
	}
	if err := refuseRemovedCatalog([]byte("semantic: [")); err != nil {
		t.Errorf("malformed YAML is left to the strict decode, got %v", err)
	}
	if got := lookupPath(map[string]any{"a": "leaf"}, "a", "b"); got != nil {
		t.Errorf("a path through a scalar resolved to %v", got)
	}
}
