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
	Region          string        `yaml:"region"`
	Endpoint        string        `yaml:"endpoint"`
	AccessKeyID     string        `yaml:"access_key_id"`
	SecretAccessKey string        `yaml:"secret_access_key"`
	SessionToken    string        `yaml:"session_token"`
	Profile         string        `yaml:"profile"`
	UsePathStyle    bool          `yaml:"use_path_style"`
	Timeout         time.Duration `yaml:"timeout"`
	DisableSSL      bool          `yaml:"disable_ssl"`
	ReadOnly        bool          `yaml:"read_only"`
	MaxGetSize      int64         `yaml:"max_get_size"`
	MaxPutSize      int64         `yaml:"max_put_size"`
	ConnectionName  string        `yaml:"connection_name"`
	BucketPrefix    string        `yaml:"bucket_prefix"`
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
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxGetSize == 0 {
		cfg.MaxGetSize = 10 * 1024 * 1024 // 10MB
	}
	if cfg.MaxPutSize == 0 {
		cfg.MaxPutSize = 100 * 1024 * 1024 // 100MB
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
	return s3tools.NewToolkit(client, opts...)
}

// Kind returns the toolkit kind.
func (t *Toolkit) Kind() string {
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

// RegisterTools registers S3 tools with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.s3Toolkit != nil {
		t.s3Toolkit.RegisterAll(s)
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
		"s3_list_connections",
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
		return t.client.Close()
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
