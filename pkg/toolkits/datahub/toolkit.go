// Package datahub provides a DataHub toolkit adapter for the MCP data platform.
package datahub

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// defaultTimeout is the default HTTP timeout for DataHub requests.
	defaultTimeout = 30 * time.Second

	// defaultDataHubLimit is the default number of results returned.
	defaultDataHubLimit = 10

	// defaultMaxLimit is the maximum number of results allowed.
	defaultMaxLimit = 100

	// defaultMaxLineageDepth is the maximum lineage traversal depth.
	defaultMaxLineageDepth = 5
)

// Config holds DataHub toolkit configuration.
type Config struct {
	URL             string            `yaml:"url"`
	Token           string            `yaml:"token"`
	Timeout         time.Duration     `yaml:"timeout"`
	DefaultLimit    int               `yaml:"default_limit"`
	MaxLimit        int               `yaml:"max_limit"`
	MaxLineageDepth int               `yaml:"max_lineage_depth"`
	ConnectionName  string            `yaml:"connection_name"`
	Debug           bool              `yaml:"debug"` // Enable debug logging
	Descriptions    map[string]string `yaml:"descriptions"`
}

// Toolkit wraps mcp-datahub toolkit for the platform.
type Toolkit struct {
	name           string
	config         Config
	client         *dhclient.Client
	datahubToolkit *dhtools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// New creates a new DataHub toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	cfg = applyDefaults(name, cfg)

	client, err := createClient(cfg)
	if err != nil {
		return nil, err
	}

	datahubToolkit := createToolkit(client, cfg)

	return &Toolkit{
		name:           name,
		config:         cfg,
		client:         client,
		datahubToolkit: datahubToolkit,
	}, nil
}

// validateConfig validates the required configuration fields.
func validateConfig(cfg Config) error {
	if cfg.URL == "" {
		return fmt.Errorf("datahub URL is required")
	}
	return nil
}

// applyDefaults applies default values to the configuration.
func applyDefaults(name string, cfg Config) Config {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = defaultDataHubLimit
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = defaultMaxLimit
	}
	if cfg.MaxLineageDepth == 0 {
		cfg.MaxLineageDepth = defaultMaxLineageDepth
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return cfg
}

// createClient creates a new DataHub client from the configuration.
func createClient(cfg Config) (*dhclient.Client, error) {
	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = cfg.URL
	clientCfg.Token = cfg.Token
	clientCfg.Timeout = cfg.Timeout
	clientCfg.DefaultLimit = cfg.DefaultLimit
	clientCfg.MaxLimit = cfg.MaxLimit
	clientCfg.MaxLineageDepth = cfg.MaxLineageDepth
	clientCfg.Debug = cfg.Debug

	client, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client: %w", err)
	}
	return client, nil
}

// toDataHubToolNames converts a generic string map to typed ToolName keys.
func toDataHubToolNames(m map[string]string) map[dhtools.ToolName]string {
	if m == nil {
		return nil
	}
	result := make(map[dhtools.ToolName]string, len(m))
	for k, v := range m {
		result[dhtools.ToolName(k)] = v
	}
	return result
}

// createToolkit creates the mcp-datahub toolkit.
func createToolkit(client *dhclient.Client, cfg Config) *dhtools.Toolkit {
	var opts []dhtools.ToolkitOption
	if len(cfg.Descriptions) > 0 {
		opts = append(opts, dhtools.WithDescriptions(toDataHubToolNames(cfg.Descriptions)))
	}
	return dhtools.NewToolkit(client, dhtools.Config{
		DefaultLimit:    cfg.DefaultLimit,
		MaxLimit:        cfg.MaxLimit,
		MaxLineageDepth: cfg.MaxLineageDepth,
		Debug:           cfg.Debug,
	}, opts...)
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return "datahub"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name for audit logging.
func (t *Toolkit) Connection() string {
	return t.config.ConnectionName
}

// RegisterTools registers DataHub tools with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.datahubToolkit != nil {
		t.datahubToolkit.RegisterAll(s)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (*Toolkit) Tools() []string {
	return []string{
		"datahub_search",
		"datahub_get_entity",
		"datahub_get_schema",
		"datahub_get_lineage",
		"datahub_get_column_lineage",
		"datahub_get_queries",
		"datahub_get_glossary_term",
		"datahub_list_tags",
		"datahub_list_domains",
		"datahub_list_data_products",
		"datahub_get_data_product",
		"datahub_list_connections",
	}
}

// SetSemanticProvider sets the semantic metadata provider for enrichment.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider sets the query execution provider for enrichment.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// Close releases resources.
func (t *Toolkit) Close() error {
	if t.client != nil {
		if err := t.client.Close(); err != nil {
			return fmt.Errorf("closing datahub client: %w", err)
		}
	}
	return nil
}

// Client returns the underlying DataHub client for direct use.
func (t *Toolkit) Client() *dhclient.Client {
	return t.client
}

// Config returns the toolkit configuration.
func (t *Toolkit) Config() Config {
	return t.config
}

// Verify interface compliance.
var _ interface {
	Kind() string
	Name() string
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
