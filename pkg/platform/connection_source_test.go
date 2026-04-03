package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionSourceMap_AddAndForConnection(t *testing.T) {
	m := NewConnectionSourceMap()

	src := ConnectionSource{
		Kind:              "trino",
		Name:              "prod",
		DataHubSourceName: "trino",
		CatalogMapping:    map[string]string{"rdbms": "postgres"},
	}
	m.Add(src)

	got := m.ForConnection("trino", "prod")
	require.NotNil(t, got)
	assert.Equal(t, "trino", got.DataHubSourceName)
	assert.Equal(t, "postgres", got.CatalogMapping["rdbms"])

	// Non-existent connection returns nil.
	assert.Nil(t, m.ForConnection("trino", "missing"))
	assert.Nil(t, m.ForConnection("s3", "prod"))
}

func TestConnectionSourceMap_ForConnectionName(t *testing.T) {
	m := NewConnectionSourceMap()
	m.Add(ConnectionSource{Kind: "trino", Name: "analytics", DataHubSourceName: "trino"})
	m.Add(ConnectionSource{Kind: "s3", Name: "lake", DataHubSourceName: "s3"})

	got := m.ForConnectionName("analytics")
	require.NotNil(t, got)
	assert.Equal(t, "trino", got.Kind)

	got = m.ForConnectionName("lake")
	require.NotNil(t, got)
	assert.Equal(t, "s3", got.Kind)

	// Non-existent name returns nil.
	assert.Nil(t, m.ForConnectionName("missing"))
}

func TestConnectionSourceMap_ConnectionsForSource(t *testing.T) {
	m := NewConnectionSourceMap()
	m.Add(ConnectionSource{Kind: "trino", Name: "prod", DataHubSourceName: "trino"})
	m.Add(ConnectionSource{Kind: "trino", Name: "staging", DataHubSourceName: "trino"})
	m.Add(ConnectionSource{Kind: "s3", Name: "lake", DataHubSourceName: "s3"})

	trinoCons := m.ConnectionsForSource("trino")
	assert.Len(t, trinoCons, 2)
	assert.Equal(t, "prod", trinoCons[0].Name)
	assert.Equal(t, "staging", trinoCons[1].Name)

	s3Cons := m.ConnectionsForSource("s3")
	assert.Len(t, s3Cons, 1)

	// Non-existent source returns nil.
	assert.Nil(t, m.ConnectionsForSource("unknown"))
}

func TestConnectionSourceMap_ConnectionsForURN(t *testing.T) {
	m := NewConnectionSourceMap()
	m.Add(ConnectionSource{Kind: "trino", Name: "prod", DataHubSourceName: "trino"})

	conns := m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)")
	assert.Len(t, conns, 1)
	assert.Equal(t, "prod", conns[0].Name)

	// Non-matching platform returns nil.
	assert.Nil(t, m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:postgres,catalog.schema.table,PROD)"))

	// Invalid URN returns nil.
	assert.Nil(t, m.ConnectionsForURN("not-a-urn"))
}

func TestExtractPlatformFromURN(t *testing.T) {
	tests := []struct {
		name     string
		urn      string
		expected string
	}{
		{
			name:     "standard dataset URN",
			urn:      "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.table,PROD)",
			expected: "trino",
		},
		{
			name:     "postgres platform",
			urn:      "urn:li:dataset:(urn:li:dataPlatform:postgres,db.schema.table,PROD)",
			expected: "postgres",
		},
		{
			name:     "platform only",
			urn:      "urn:li:dataPlatform:s3",
			expected: "s3",
		},
		{
			name:     "no platform prefix",
			urn:      "urn:li:dataset:(urn:li:dataFlow:airflow,flow1,PROD)",
			expected: "",
		},
		{
			name:     "empty string",
			urn:      "",
			expected: "",
		},
		{
			name:     "platform with closing paren",
			urn:      "urn:li:dataset:(urn:li:dataPlatform:hive)",
			expected: "hive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, extractPlatformFromURN(tt.urn))
		})
	}
}

func TestConnectionSourceFromInstance(t *testing.T) {
	t.Run("with datahub_source_name and catalog_mapping", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:        "trino",
			Name:        "prod",
			Description: "Production Trino",
			Config: map[string]any{
				"host":                "trino.local",
				"datahub_source_name": "custom_trino",
				"catalog_mapping": map[string]any{
					"rdbms": "postgres",
					"hive":  "hdfs",
				},
			},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Equal(t, "trino", src.Kind)
		assert.Equal(t, "prod", src.Name)
		assert.Equal(t, "custom_trino", src.DataHubSourceName)
		assert.Equal(t, "Production Trino", src.Description)
		assert.Equal(t, "postgres", src.CatalogMapping["rdbms"])
		assert.Equal(t, "hdfs", src.CatalogMapping["hive"])
	})

	t.Run("without datahub_source_name uses default", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:   "s3",
			Name:   "lake",
			Config: map[string]any{"bucket": "my-bucket"},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Equal(t, "s3", src.DataHubSourceName)
		assert.Nil(t, src.CatalogMapping)
	})

	t.Run("empty config uses defaults", func(t *testing.T) {
		inst := ConnectionInstance{
			Kind:   "trino",
			Name:   "dev",
			Config: map[string]any{},
		}

		src := ConnectionSourceFromInstance(inst)
		assert.Equal(t, "trino", src.DataHubSourceName)
	})
}

func TestDefaultSourceNameForKind(t *testing.T) {
	assert.Equal(t, "trino", defaultSourceNameForKind("trino"))
	assert.Equal(t, "s3", defaultSourceNameForKind("s3"))
	assert.Equal(t, "", defaultSourceNameForKind("unknown"))
	assert.Equal(t, "", defaultSourceNameForKind(""))
}

func TestConnectionSourceMap_Nil(t *testing.T) {
	var m *ConnectionSourceMap

	assert.Nil(t, m.ForConnection("trino", "prod"))
	assert.Nil(t, m.ForConnectionName("prod"))
	assert.Nil(t, m.ConnectionsForSource("trino"))
	assert.Nil(t, m.ConnectionsForURN("urn:li:dataset:(urn:li:dataPlatform:trino,c.s.t,PROD)"))
}
