// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Config holds Trino toolkit configuration.
type Config struct {
	Host           string        `yaml:"host"`
	Port           int           `yaml:"port"`
	User           string        `yaml:"user"`
	Password       string        `yaml:"password"`
	Catalog        string        `yaml:"catalog"`
	Schema         string        `yaml:"schema"`
	SSL            bool          `yaml:"ssl"`
	SSLVerify      bool          `yaml:"ssl_verify"`
	Timeout        time.Duration `yaml:"timeout"`
	DefaultLimit   int           `yaml:"default_limit"`
	MaxLimit       int           `yaml:"max_limit"`
	ReadOnly       bool          `yaml:"read_only"`
	ConnectionName string        `yaml:"connection_name"`
}

// Toolkit wraps mcp-trino toolkit for the platform.
type Toolkit struct {
	name         string
	config       Config
	client       *trinoclient.Client
	trinoToolkit *trinotools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider
	middlewareChain  *middleware.Chain
}

// New creates a new Trino toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("trino host is required")
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("trino user is required")
	}
	if cfg.Port == 0 {
		if cfg.SSL {
			cfg.Port = 443
		} else {
			cfg.Port = 8080
		}
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = 1000
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = 10000
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}

	clientCfg := trinoclient.Config{
		Host:      cfg.Host,
		Port:      cfg.Port,
		User:      cfg.User,
		Password:  cfg.Password,
		Catalog:   cfg.Catalog,
		Schema:    cfg.Schema,
		SSL:       cfg.SSL,
		SSLVerify: cfg.SSLVerify,
		Timeout:   cfg.Timeout,
		Source:    "mcp-data-platform",
	}

	client, err := trinoclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating trino client: %w", err)
	}

	// Create the mcp-trino toolkit
	trinoToolkit := trinotools.NewToolkit(client, trinotools.Config{
		DefaultLimit: cfg.DefaultLimit,
		MaxLimit:     cfg.MaxLimit,
	})

	return &Toolkit{
		name:         name,
		config:       cfg,
		client:       client,
		trinoToolkit: trinoToolkit,
	}, nil
}

// Kind returns the toolkit kind.
func (t *Toolkit) Kind() string {
	return "trino"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// RegisterTools registers Trino tools with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.trinoToolkit != nil {
		t.trinoToolkit.RegisterAll(s)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (t *Toolkit) Tools() []string {
	return []string{
		"trino_query",
		"trino_explain",
		"trino_list_catalogs",
		"trino_list_schemas",
		"trino_list_tables",
		"trino_describe_table",
		"trino_list_connections",
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

// Client returns the underlying Trino client for direct use.
func (t *Toolkit) Client() *trinoclient.Client {
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
