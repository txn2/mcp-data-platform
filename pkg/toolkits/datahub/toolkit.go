// Package datahub provides a DataHub toolkit adapter for the MCP data platform.
package datahub

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Config holds DataHub toolkit configuration.
type Config struct {
	URL             string        `yaml:"url"`
	Token           string        `yaml:"token"`
	Timeout         time.Duration `yaml:"timeout"`
	DefaultLimit    int           `yaml:"default_limit"`
	MaxLimit        int           `yaml:"max_limit"`
	MaxLineageDepth int           `yaml:"max_lineage_depth"`
	ConnectionName  string        `yaml:"connection_name"`
}

// Toolkit wraps mcp-datahub toolkit for the platform.
type Toolkit struct {
	name           string
	config         Config
	client         *dhclient.Client
	datahubToolkit *dhtools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider
	middlewareChain  *middleware.Chain
}

// New creates a new DataHub toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("datahub URL is required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = 10
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = 100
	}
	if cfg.MaxLineageDepth == 0 {
		cfg.MaxLineageDepth = 5
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = cfg.URL
	clientCfg.Token = cfg.Token
	clientCfg.Timeout = cfg.Timeout
	clientCfg.DefaultLimit = cfg.DefaultLimit
	clientCfg.MaxLimit = cfg.MaxLimit
	clientCfg.MaxLineageDepth = cfg.MaxLineageDepth

	client, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client: %w", err)
	}

	// Create the mcp-datahub toolkit
	datahubToolkit := dhtools.NewToolkit(client, dhtools.Config{
		DefaultLimit:    cfg.DefaultLimit,
		MaxLimit:        cfg.MaxLimit,
		MaxLineageDepth: cfg.MaxLineageDepth,
	})

	return &Toolkit{
		name:           name,
		config:         cfg,
		client:         client,
		datahubToolkit: datahubToolkit,
	}, nil
}

// Kind returns the toolkit kind.
func (t *Toolkit) Kind() string {
	return "datahub"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// RegisterTools registers DataHub tools with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.datahubToolkit != nil {
		t.datahubToolkit.RegisterAll(s)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (t *Toolkit) Tools() []string {
	return []string{
		"datahub_search",
		"datahub_get_entity",
		"datahub_get_schema",
		"datahub_get_lineage",
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

// SetMiddleware sets the middleware chain for tool handlers.
func (t *Toolkit) SetMiddleware(chain *middleware.Chain) {
	t.middlewareChain = chain
}

// Close releases resources.
func (t *Toolkit) Close() error {
	if t.client != nil {
		return t.client.Close()
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
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	SetMiddleware(chain *middleware.Chain)
	Close() error
} = (*Toolkit)(nil)
