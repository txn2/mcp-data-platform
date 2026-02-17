// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// defaultQueryLimit is the default number of rows returned by queries.
	defaultQueryLimit = 1000

	// defaultMaxLimit is the maximum number of rows allowed per query.
	defaultMaxLimit = 10000

	// defaultTrinoTimeout is the default query timeout.
	defaultTrinoTimeout = 120 * time.Second

	// defaultSSLPort is the default port when SSL is enabled.
	defaultSSLPort = 443

	// defaultPlainPort is the default port when SSL is disabled.
	defaultPlainPort = 8080
)

// Config holds Trino toolkit configuration.
type Config struct {
	Host           string                      `yaml:"host"`
	Port           int                         `yaml:"port"`
	User           string                      `yaml:"user"`
	Password       string                      `yaml:"password"`
	Catalog        string                      `yaml:"catalog"`
	Schema         string                      `yaml:"schema"`
	SSL            bool                        `yaml:"ssl"`
	SSLVerify      bool                        `yaml:"ssl_verify"`
	Timeout        time.Duration               `yaml:"timeout"`
	DefaultLimit   int                         `yaml:"default_limit"`
	MaxLimit       int                         `yaml:"max_limit"`
	ReadOnly       bool                        `yaml:"read_only"`
	ConnectionName string                      `yaml:"connection_name"`
	Descriptions   map[string]string           `yaml:"descriptions"`
	Annotations    map[string]AnnotationConfig `yaml:"annotations"`

	// ProgressEnabled enables progress notifications for query execution.
	// Injected by the platform from progress.enabled config.
	ProgressEnabled bool `yaml:"progress_enabled"`

	// Elicitation configures user confirmation for expensive operations.
	// Injected by the platform from elicitation config.
	Elicitation ElicitationConfig `yaml:"elicitation"`
}

// ElicitationConfig configures elicitation triggers for the Trino toolkit.
type ElicitationConfig struct {
	// Enabled is the master switch for all elicitation features.
	Enabled bool `yaml:"enabled"`

	// CostEstimation configures query cost estimation and confirmation.
	CostEstimation CostEstimationConfig `yaml:"cost_estimation"`

	// PIIConsent configures PII access consent.
	PIIConsent PIIConsentConfig `yaml:"pii_consent"`
}

// CostEstimationConfig configures query cost estimation.
type CostEstimationConfig struct {
	Enabled      bool  `yaml:"enabled"`
	RowThreshold int64 `yaml:"row_threshold"`
}

// PIIConsentConfig configures PII access consent.
type PIIConsentConfig struct {
	Enabled bool `yaml:"enabled"`
}

// Toolkit wraps mcp-trino toolkit for the platform.
type Toolkit struct {
	name         string
	config       Config
	client       *trinoclient.Client
	trinoToolkit *trinotools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider

	// elicitation holds the middleware so providers can be propagated after init.
	elicitation *ElicitationMiddleware
}

// New creates a new Trino toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	cfg = applyDefaults(name, cfg)

	client, err := createClient(cfg)
	if err != nil {
		return nil, err
	}

	t := &Toolkit{
		name:   name,
		config: cfg,
		client: client,
	}

	// Create elicitation middleware before toolkit so it can be passed as an option.
	if cfg.Elicitation.Enabled {
		t.elicitation = &ElicitationMiddleware{
			client: client,
			config: cfg.Elicitation,
		}
	}

	t.trinoToolkit = createToolkit(client, cfg, t.elicitation)

	return t, nil
}

// validateConfig validates the required configuration fields.
func validateConfig(cfg Config) error {
	if cfg.Host == "" {
		return fmt.Errorf("trino host is required")
	}
	if cfg.User == "" {
		return fmt.Errorf("trino user is required")
	}
	return nil
}

// applyDefaults applies default values to the configuration.
func applyDefaults(name string, cfg Config) Config {
	if cfg.Port == 0 {
		cfg.Port = defaultPort(cfg.SSL)
	}
	if cfg.DefaultLimit == 0 {
		cfg.DefaultLimit = defaultQueryLimit
	}
	if cfg.MaxLimit == 0 {
		cfg.MaxLimit = defaultMaxLimit
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTrinoTimeout
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return cfg
}

// defaultPort returns the default port based on SSL setting.
func defaultPort(ssl bool) int {
	if ssl {
		return defaultSSLPort
	}
	return defaultPlainPort
}

// createClient creates a new Trino client from the configuration.
func createClient(cfg Config) (*trinoclient.Client, error) {
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
	return client, nil
}

// toTrinoToolNames converts a generic string map to typed ToolName keys.
func toTrinoToolNames(m map[string]string) map[trinotools.ToolName]string {
	if m == nil {
		return nil
	}
	result := make(map[trinotools.ToolName]string, len(m))
	for k, v := range m {
		result[trinotools.ToolName(k)] = v
	}
	return result
}

// createToolkit creates the mcp-trino toolkit with appropriate options.
func createToolkit(client *trinoclient.Client, cfg Config, elicit *ElicitationMiddleware) *trinotools.Toolkit {
	var opts []trinotools.ToolkitOption

	// Add read-only interceptor if configured
	if cfg.ReadOnly {
		opts = append(opts, trinotools.WithQueryInterceptor(NewReadOnlyInterceptor()))
	}

	// Add description overrides if configured
	if len(cfg.Descriptions) > 0 {
		opts = append(opts, trinotools.WithDescriptions(toTrinoToolNames(cfg.Descriptions)))
	}

	// Add annotation overrides if configured
	if len(cfg.Annotations) > 0 {
		opts = append(opts, trinotools.WithAnnotations(toTrinoAnnotations(cfg.Annotations)))
	}

	// Add progress notifier injector if enabled
	if cfg.ProgressEnabled {
		opts = append(opts, trinotools.WithMiddleware(&ProgressInjector{}))
	}

	// Add elicitation middleware if enabled
	if elicit != nil {
		opts = append(opts, trinotools.WithMiddleware(elicit))
	}

	return trinotools.NewToolkit(client, trinotools.Config{
		DefaultLimit: cfg.DefaultLimit,
		MaxLimit:     cfg.MaxLimit,
	}, opts...)
}

// toTrinoAnnotations converts config annotation overrides to mcp-trino ToolAnnotations.
func toTrinoAnnotations(m map[string]AnnotationConfig) map[trinotools.ToolName]*mcp.ToolAnnotations {
	if m == nil {
		return nil
	}
	result := make(map[trinotools.ToolName]*mcp.ToolAnnotations, len(m))
	for k, v := range m {
		result[trinotools.ToolName(k)] = annotationConfigToMCP(v)
	}
	return result
}

// annotationConfigToMCP converts an AnnotationConfig to an mcp.ToolAnnotations.
func annotationConfigToMCP(cfg AnnotationConfig) *mcp.ToolAnnotations {
	ann := &mcp.ToolAnnotations{}
	if cfg.ReadOnlyHint != nil {
		ann.ReadOnlyHint = *cfg.ReadOnlyHint
	}
	if cfg.DestructiveHint != nil {
		ann.DestructiveHint = cfg.DestructiveHint
	}
	if cfg.IdempotentHint != nil {
		ann.IdempotentHint = *cfg.IdempotentHint
	}
	if cfg.OpenWorldHint != nil {
		ann.OpenWorldHint = cfg.OpenWorldHint
	}
	return ann
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return "trino"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name for audit logging.
func (t *Toolkit) Connection() string {
	return t.config.ConnectionName
}

// RegisterTools registers Trino tools with the MCP server.
// The platform provides a unified list_connections tool, so the per-toolkit
// trino_list_connections is excluded.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.trinoToolkit != nil {
		t.trinoToolkit.Register(s,
			trinotools.ToolQuery,
			trinotools.ToolExplain,
			trinotools.ToolListCatalogs,
			trinotools.ToolListSchemas,
			trinotools.ToolListTables,
			trinotools.ToolDescribeTable,
		)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (*Toolkit) Tools() []string {
	return []string{
		"trino_query",
		"trino_explain",
		"trino_list_catalogs",
		"trino_list_schemas",
		"trino_list_tables",
		"trino_describe_table",
	}
}

// SetSemanticProvider sets the semantic metadata provider for enrichment.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
	if t.elicitation != nil {
		t.elicitation.SetSemanticProvider(provider)
	}
}

// SetQueryProvider sets the query execution provider for enrichment.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// Close releases resources.
func (t *Toolkit) Close() error {
	if t.client != nil {
		if err := t.client.Close(); err != nil {
			return fmt.Errorf("closing trino client: %w", err)
		}
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
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
