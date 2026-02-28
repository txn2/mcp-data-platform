// Package s3 provides an S3 toolkit adapter for the MCP data platform.
package s3

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3client "github.com/txn2/mcp-s3/pkg/client"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Config holds S3 toolkit configuration.
type Config struct {
	Region          string                      `yaml:"region"`
	Endpoint        string                      `yaml:"endpoint"`
	AccessKeyID     string                      `yaml:"access_key_id"`
	SecretAccessKey string                      `yaml:"secret_access_key"`
	SessionToken    string                      `yaml:"session_token"` // #nosec G117 -- S3 session token from admin YAML config
	Profile         string                      `yaml:"profile"`
	UsePathStyle    bool                        `yaml:"use_path_style"`
	Timeout         time.Duration               `yaml:"timeout"`
	DisableSSL      bool                        `yaml:"disable_ssl"`
	ReadOnly        bool                        `yaml:"read_only"`
	MaxGetSize      int64                       `yaml:"max_get_size"`
	MaxPutSize      int64                       `yaml:"max_put_size"`
	ConnectionName  string                      `yaml:"connection_name"`
	BucketPrefix    string                      `yaml:"bucket_prefix"`
	Titles          map[string]string           `yaml:"titles"`
	Descriptions    map[string]string           `yaml:"descriptions"`
	Annotations     map[string]AnnotationConfig `yaml:"annotations"`
}

// Toolkit wraps mcp-s3 toolkit for the platform.
type Toolkit struct {
	name      string
	config    Config
	client    *s3client.Client
	s3Toolkit *s3tools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// New creates a new S3 toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	cfg = applyDefaults(name, cfg)

	client, err := createClient(cfg)
	if err != nil {
		return nil, err
	}

	s3Toolkit := createToolkit(client, cfg)

	return &Toolkit{
		name:      name,
		config:    cfg,
		client:    client,
		s3Toolkit: s3Toolkit,
	}, nil
}

// applyDefaults applies default values to the configuration.
func applyDefaults(name string, cfg Config) Config {
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxGetSize == 0 {
		cfg.MaxGetSize = DefaultMaxGetSize
	}
	if cfg.MaxPutSize == 0 {
		cfg.MaxPutSize = DefaultMaxPutSize
	}
	if cfg.ConnectionName == "" {
		cfg.ConnectionName = name
	}
	return cfg
}

// createClient creates a new S3 client from the configuration.
func createClient(cfg Config) (*s3client.Client, error) {
	clientCfg := &s3client.Config{
		Region:          cfg.Region,
		Endpoint:        cfg.Endpoint,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretAccessKey,
		SessionToken:    cfg.SessionToken,
		Profile:         cfg.Profile,
		UsePathStyle:    cfg.UsePathStyle,
		Timeout:         cfg.Timeout,
		Name:            cfg.ConnectionName,
		DisableSSL:      cfg.DisableSSL,
	}

	ctx := context.Background()
	client, err := s3client.New(ctx, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}
	return client, nil
}

// createToolkit creates the mcp-s3 toolkit with appropriate options.
func createToolkit(client *s3client.Client, cfg Config) *s3tools.Toolkit {
	var opts []s3tools.Option
	opts = append(opts, s3tools.WithReadOnly(cfg.ReadOnly))
	if cfg.MaxGetSize > 0 {
		opts = append(opts, s3tools.WithMaxGetSize(cfg.MaxGetSize))
	}
	if cfg.MaxPutSize > 0 {
		opts = append(opts, s3tools.WithMaxPutSize(cfg.MaxPutSize))
	}
	if len(cfg.Titles) > 0 {
		opts = append(opts, s3tools.WithTitles(toS3ToolNames(cfg.Titles)))
	}
	if len(cfg.Descriptions) > 0 {
		opts = append(opts, s3tools.WithDescriptions(toS3ToolNames(cfg.Descriptions)))
	}
	if len(cfg.Annotations) > 0 {
		opts = append(opts, s3tools.WithAnnotations(toS3Annotations(cfg.Annotations)))
	}
	return s3tools.NewToolkit(client, opts...)
}

// toS3ToolNames converts a generic string map to typed ToolName keys.
func toS3ToolNames(m map[string]string) map[s3tools.ToolName]string {
	if m == nil {
		return nil
	}
	result := make(map[s3tools.ToolName]string, len(m))
	for k, v := range m {
		result[s3tools.ToolName(k)] = v
	}
	return result
}

// toS3Annotations converts config annotation overrides to mcp-s3 ToolAnnotations.
func toS3Annotations(m map[string]AnnotationConfig) map[s3tools.ToolName]*mcp.ToolAnnotations {
	if m == nil {
		return nil
	}
	result := make(map[s3tools.ToolName]*mcp.ToolAnnotations, len(m))
	for k, v := range m {
		result[s3tools.ToolName(k)] = annotationConfigToMCP(v)
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
	return "s3"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name for audit logging.
func (t *Toolkit) Connection() string {
	return t.config.ConnectionName
}

// s3ReadTools lists the read-only S3 tools registered by the platform.
// This excludes s3_list_connections (replaced by the unified list_connections).
var s3ReadTools = []s3tools.ToolName{
	s3tools.ToolListBuckets,
	s3tools.ToolListObjects,
	s3tools.ToolGetObject,
	s3tools.ToolGetObjectMetadata,
	s3tools.ToolPresignURL,
}

// RegisterTools registers S3 tools with the MCP server.
// The platform provides a unified list_connections tool, so the per-toolkit
// s3_list_connections is excluded.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.s3Toolkit == nil {
		return
	}
	t.s3Toolkit.Register(s, s3ReadTools...)
	if !t.config.ReadOnly {
		t.s3Toolkit.Register(s, s3tools.WriteTools()...)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (t *Toolkit) Tools() []string {
	tools := []string{
		"s3_list_buckets",
		"s3_list_objects",
		"s3_get_object",
		"s3_get_object_metadata",
		"s3_presign_url",
	}

	if !t.config.ReadOnly {
		tools = append(tools,
			"s3_put_object",
			"s3_delete_object",
			"s3_copy_object",
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
			return fmt.Errorf("closing s3 client: %w", err)
		}
	}
	return nil
}

// Client returns the underlying S3 client for direct use.
func (t *Toolkit) Client() *s3client.Client {
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
