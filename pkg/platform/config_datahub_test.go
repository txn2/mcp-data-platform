package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// upgradingConfig is the shape of a configuration that ran DataHub: the
// toolkit with two instances, the semantic layer naming one with its URN
// mapping, and the enrichment, storage and apply settings beside them.
const upgradingConfig = `
config:
  strict: true
semantic:
  provider: datahub
  instance: primary
  urn_mapping:
    platform: postgres
    catalog_mapping:
      rdbms: warehouse
  lineage:
    enabled: true
query:
  provider: noop
  urn_mapping:
    catalog_mapping:
      warehouse: rdbms
storage:
  provider: s3
  instance: primary
enrichment:
  datahub_query_enrichment: true
  datahub_storage_enrichment: false
  s3_semantic_enrichment: true
  search_schema_preview: true
  schema_preview_max_columns: 20
knowledge:
  apply:
    datahub_connection: primary
toolkits:
  datahub:
    enabled: true
    default: other
    config:
      timeout: 30s
      token: shared-token
    instances:
      other:
        url: http://other:8080
      primary:
        endpoint: " http://datahub-gms:8080 "
`

// A configuration upgrading from DataHub starts, strict parsing included: it
// names the DataHub semantic.instance chose, with the URN mapping the import
// places datasets by, the toolkit itself is gone, and the platform catalog is
// the semantic layer.
func TestLoadConfigReadsTheDataHubAnUpgradeImports(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte(upgradingConfig))
	require.NoError(t, err)
	require.NotNil(t, cfg.DataHubImport)
	assert.Equal(t, DataHubImport{
		URL: "http://datahub-gms:8080", Token: "shared-token",
		Platform: "postgres", CatalogMapping: map[string]string{"rdbms": "warehouse"},
	}, *cfg.DataHubImport)
	assert.NotContains(t, cfg.Toolkits, kindDataHub)
	assert.Empty(t, cfg.Semantic.Provider)
}

func TestDataHubSourceInstance(t *testing.T) {
	for name, tc := range map[string]struct {
		yaml string
		want *DataHubImport
	}{
		"the kind's default": {
			yaml: "toolkits:\n  datahub:\n    enabled: true\n    default: b\n    instances:\n      a: {url: 'http://a'}\n      b: {url: 'http://b', token: t}\n",
			want: &DataHubImport{URL: "http://b", Token: "t"},
		},
		"the first by name, read by the semantic layer": {
			yaml: "semantic:\n  provider: datahub\ntoolkits:\n  datahub:\n    enabled: false\n    instances:\n      z: {url: 'http://z'}\n      a: {url: 'http://a'}\n",
			want: &DataHubImport{URL: "http://a"},
		},
		"an enabled toolkit expanded from the environment": {
			yaml: "toolkits:\n  datahub:\n    enabled: 'true'\n    instances:\n      a: {url: 'http://a'}\n",
			want: &DataHubImport{URL: "http://a"},
		},
		"a toolkit nothing read": {
			yaml: "toolkits:\n  datahub:\n    enabled: false\n    instances:\n      a: {url: 'http://a'}\n",
		},
		"an instance with no address": {
			yaml: "toolkits:\n  datahub:\n    enabled: true\n    instances:\n      a: {token: t}\n",
		},
		"no instances": {yaml: "toolkits:\n  datahub:\n    enabled: true\n"},
		"an enabled toolkit with no instances map": {yaml: "toolkits:\n  datahub:\n    enabled: true\n    instances: []\n"},
		"no DataHub": {yaml: "semantic:\n  provider: noop\n"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, readDataHubUpgrade([]byte(tc.yaml)).source)
		})
	}
}

// Each DataHub setting is reported, and only those: a setting the catalog
// still has is not, and YAML that does not parse is left to the decode.
func TestReadDataHubUpgradeFindsEachSetting(t *testing.T) {
	up := readDataHubUpgrade([]byte(upgradingConfig))
	assert.Equal(t, []string{
		"enrichment.datahub_query_enrichment", "enrichment.datahub_storage_enrichment",
		"enrichment.s3_semantic_enrichment", "enrichment.schema_preview_max_columns",
		"enrichment.search_schema_preview", "knowledge.apply.datahub_connection",
		"query.urn_mapping", "semantic.instance", "semantic.lineage", "semantic.provider: datahub",
		"semantic.urn_mapping", "storage", "toolkits.datahub",
	}, up.found)

	assert.Empty(t, readDataHubUpgrade([]byte("semantic:\n  provider: noop\nenrichment:\n  unwrap_json: true\n")).found)
	assert.Empty(t, readDataHubUpgrade([]byte("semantic: [")).found)
}

// Strict parsing still refuses a key that is not one of DataHub's.
func TestStrictParsingStillRefusesOtherUnknownKeys(t *testing.T) {
	_, err := LoadConfigFromBytes([]byte("config:\n  strict: true\nsemantic:\n  urn_mapping: {}\n  typo: 1\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo")
	assert.NotContains(t, err.Error(), "urn_mapping")
}

// A configuration with no DataHub is decoded as it was.
func TestNoDataHubLeavesTheConfigurationAlone(t *testing.T) {
	cfg, err := LoadConfigFromBytes([]byte("semantic:\n  provider: noop\n"))
	require.NoError(t, err)
	assert.Nil(t, cfg.DataHubImport)
	assert.Equal(t, providerNoop, cfg.Semantic.Provider)
	assert.Equal(t, "none: toolkits.datahub names no url", importAddress(nil))
	assert.Nil(t, lookupPath(map[string]any{"a": "leaf"}, "a", "b"))
}
