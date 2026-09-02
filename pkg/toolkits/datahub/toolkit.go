// Package datahub provides a DataHub toolkit adapter for the MCP data platform.
package datahub

import (
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	dhtools "github.com/txn2/mcp-datahub/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const (
	// defaultTimeout is the default HTTP timeout for DataHub requests.
	defaultTimeout = 30 * time.Second

	// defaultDataHubLimit is the default number of results returned.
	defaultDataHubLimit = 10

	// defaultMaxLimit is the maximum number of results allowed.
	defaultMaxLimit = 100

	// DataHub tool names. The full set is named here even though only
	// a subset crosses goconst's literal-repetition threshold — keeping
	// the Tools() list uniformly constant-driven avoids a visually mixed
	// list of constants and bare strings.
	toolGetLineage = "datahub_get_lineage"
	toolBrowse     = "datahub_browse"
	toolCreate     = "datahub_create"
	toolUpdate     = "datahub_update"
	toolDelete     = "datahub_delete"

	// defaultMaxLineageDepth is the maximum lineage traversal depth.
	defaultMaxLineageDepth = 5
)

// Config holds DataHub toolkit configuration.
type Config struct {
	URL             string                      `yaml:"url"`
	Token           string                      `yaml:"token"`
	Timeout         time.Duration               `yaml:"timeout"`
	DefaultLimit    int                         `yaml:"default_limit"`
	MaxLimit        int                         `yaml:"max_limit"`
	MaxLineageDepth int                         `yaml:"max_lineage_depth"`
	ConnectionName  string                      `yaml:"connection_name"`
	Debug           bool                        `yaml:"debug"`     // Enable debug logging
	ReadOnly        bool                        `yaml:"read_only"` // Restrict to read operations
	Titles          map[string]string           `yaml:"titles"`
	Descriptions    map[string]string           `yaml:"descriptions"`
	Annotations     map[string]AnnotationConfig `yaml:"annotations"`
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
		return errors.New("datahub URL is required")
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

// platformDescriptions are the descriptions the platform gives the DataHub
// tools it registers, in place of mcp-datahub's own, where upstream's text
// steers toward a tool the platform does not register: the by-URN reads
// (datahub_get_entity, datahub_get_schema, datahub_get_queries,
// datahub_get_glossary_term, datahub_get_data_product) are folded into fetch
// (#1590). A configured description override still wins.
var platformDescriptions = map[dhtools.ToolName]string{
	dhtools.ToolBrowse: "Navigate the DataHub catalog by platform, domain, tag, or entity type " +
		"instead of by relevance: a structured walk of what the catalog holds, paged, for when you " +
		"want the shape of a platform or a domain rather than a ranked answer. Use search for a " +
		"question. Each result carries a urn:li:... reference; read one in full with fetch, which " +
		"returns a dataset's business context, declared schema, saved queries, and query availability " +
		"in one call.",
}

// createToolkit creates the mcp-datahub toolkit.
func createToolkit(client *dhclient.Client, cfg Config) *dhtools.Toolkit {
	var opts []dhtools.ToolkitOption
	if len(cfg.Titles) > 0 {
		opts = append(opts, dhtools.WithTitles(toDataHubToolNames(cfg.Titles)))
	}
	opts = append(opts, dhtools.WithDescriptions(toolDescriptions(cfg.Descriptions)))
	if len(cfg.Annotations) > 0 {
		opts = append(opts, dhtools.WithAnnotations(toDataHubAnnotations(cfg.Annotations)))
	}
	return dhtools.NewToolkit(client, dhtools.Config{
		DefaultLimit:    cfg.DefaultLimit,
		MaxLimit:        cfg.MaxLimit,
		MaxLineageDepth: cfg.MaxLineageDepth,
		Debug:           cfg.Debug,
		WriteEnabled:    !cfg.ReadOnly,
	}, opts...)
}

// toolDescriptions merges the platform's own descriptions with the configured
// overrides, the configured text winning.
func toolDescriptions(configured map[string]string) map[dhtools.ToolName]string {
	out := make(map[dhtools.ToolName]string, len(platformDescriptions)+len(configured))
	maps.Copy(out, platformDescriptions)
	for k, v := range configured {
		out[dhtools.ToolName(k)] = v
	}
	return out
}

// toDataHubAnnotations converts config annotation overrides to mcp-datahub ToolAnnotations.
func toDataHubAnnotations(m map[string]AnnotationConfig) map[dhtools.ToolName]*mcp.ToolAnnotations {
	if m == nil {
		return nil
	}
	result := make(map[dhtools.ToolName]*mcp.ToolAnnotations, len(m))
	for k, v := range m {
		result[dhtools.ToolName(k)] = toolkit.AnnotationsToMCP(v)
	}
	return result
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

// datahubReadTools lists the read-only DataHub tools registered by the platform:
// the two that do something a reference read cannot. datahub_browse walks the
// catalog's hierarchy and datahub_get_lineage is a graph query with a direction
// and a depth. Every other upstream read is served elsewhere: datahub_list_connections
// by the unified list_connections, datahub_search by the unified search, and the
// five by-URN reads (datahub_get_entity, datahub_get_schema, datahub_get_queries,
// datahub_get_glossary_term, datahub_get_data_product) by fetch, which returns
// at least what each did for the same reference (#1590). The retirement is a
// hard cut: none of the five is registered under any alias, and a persona or
// config that still lists one grants nothing. TestRetiredReadToolsAreReplacedByFetch
// names each with the call that replaced it.
var datahubReadTools = []dhtools.ToolName{
	dhtools.ToolGetLineage,
	dhtools.ToolBrowse,
}

// RegisterTools registers DataHub tools with the MCP server.
// The platform provides a unified list_connections tool, so the per-toolkit
// datahub_list_connections is excluded.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.datahubToolkit != nil {
		t.datahubToolkit.Register(s, datahubReadTools...)
		if !t.config.ReadOnly {
			t.datahubToolkit.Register(s, dhtools.WriteTools()...)
		}
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (t *Toolkit) Tools() []string {
	tools := []string{
		toolGetLineage,
		toolBrowse,
	}

	if !t.config.ReadOnly {
		tools = append(tools,
			toolCreate,
			toolUpdate,
			toolDelete,
		)
	}

	return tools
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
