// Package s3 provides an S3 toolkit adapter for the MCP data platform.
package s3

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3client "github.com/txn2/mcp-s3/pkg/client"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Config holds S3 toolkit configuration.
type Config struct {
	Region          string                      `yaml:"region"`
	Endpoint        string                      `yaml:"endpoint"`
	PublicEndpoint  string                      `yaml:"public_endpoint"`
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
	Description     string                      `yaml:"description"`
	BucketPrefix    string                      `yaml:"bucket_prefix"`
	Titles          map[string]string           `yaml:"titles"`
	Descriptions    map[string]string           `yaml:"descriptions"`
	Annotations     map[string]AnnotationConfig `yaml:"annotations"`
}

// defaultS3Region is the region a connection that names none is signed for.
const defaultS3Region = "us-east-1"

// MultiConfig is every S3 instance a deployment declares, and which of them a
// call that names no connection means.
type MultiConfig struct {
	DefaultConnection string
	Instances         map[string]Config
}

// Toolkit is the one S3 toolkit of a deployment: every instance is a
// connection of it, routed by the `connection` argument, so two instances never
// register the same tool name twice (the SDK keeps the last registration, which
// left every earlier instance unreachable).
type Toolkit struct {
	// name is the default connection's bound name, what a call that names no
	// connection is served by and what Connection() reports.
	name      string
	config    Config
	client    *s3client.Client
	s3Toolkit *s3tools.Toolkit
	metrics   *observability.Metrics

	// descriptions is what list_connections shows for each bound name.
	descriptions map[string]string

	// connections holds, per bound name, the settings a call is bound by:
	// whether the connection is read-only, the bucket prefix it lists, and its
	// size ceilings. NewMulti enters every declared instance and AddConnection
	// enters its own, so a read_only connection added at run time refuses a
	// write exactly as a configured one does.
	connMu      sync.RWMutex
	connections map[string]connSettings

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// connSettings is the per-connection half of Config: what a call against one
// named connection is allowed to do and how much it may move.
type connSettings struct {
	readOnly     bool
	bucketPrefix string
	maxGetSize   int64
	maxPutSize   int64
}

func settingsOf(cfg Config) connSettings {
	return connSettings{
		readOnly:     cfg.ReadOnly,
		bucketPrefix: cfg.BucketPrefix,
		maxGetSize:   cfg.MaxGetSize,
		maxPutSize:   cfg.MaxPutSize,
	}
}

// New creates an S3 toolkit over one instance.
func New(name string, cfg Config) (*Toolkit, error) {
	return NewMulti(MultiConfig{DefaultConnection: name, Instances: map[string]Config{name: cfg}})
}

// NewMulti creates the toolkit over every instance. Each instance is bound by
// its connection_name (its instance name when it sets none), which is the name
// a call's `connection` argument carries, an audit row records, and a persona's
// connection rules match. The default instance serves a call naming none; with
// no default declared the alphabetically first instance is it.
func NewMulti(cfg MultiConfig) (*Toolkit, error) {
	if len(cfg.Instances) == 0 {
		return nil, errors.New("at least one s3 instance is required")
	}
	defaultName := cfg.DefaultConnection
	if defaultName == "" {
		defaultName = slices.Min(slices.Collect(maps.Keys(cfg.Instances)))
	}
	defaultCfg, ok := cfg.Instances[defaultName]
	if !ok {
		return nil, fmt.Errorf("default connection %q not found in instances", defaultName)
	}
	defaultCfg = applyDefaults(defaultName, defaultCfg)
	client, err := createClient(defaultCfg)
	if err != nil {
		return nil, fmt.Errorf("instance %s: %w", defaultName, err)
	}
	t := &Toolkit{
		name:         defaultCfg.ConnectionName,
		config:       defaultCfg,
		client:       client,
		s3Toolkit:    s3tools.NewToolkit(client),
		descriptions: map[string]string{defaultCfg.ConnectionName: defaultCfg.Description},
		connections:  map[string]connSettings{defaultCfg.ConnectionName: settingsOf(defaultCfg)},
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Instances)) {
		if name == defaultName {
			continue
		}
		instCfg := applyDefaults(name, cfg.Instances[name])
		if err := t.bind(instCfg); err != nil {
			return nil, fmt.Errorf("instance %s: %w", name, err)
		}
	}
	return t, nil
}

// bind opens a client for one instance and enters it under its bound name.
func (t *Toolkit) bind(cfg Config) error {
	client, err := createClient(cfg)
	if err != nil {
		return err
	}
	t.connMu.Lock()
	t.connections[cfg.ConnectionName] = settingsOf(cfg)
	t.descriptions[cfg.ConnectionName] = cfg.Description
	t.connMu.Unlock()
	t.s3Toolkit.AddClient(cfg.ConnectionName, client)
	return nil
}

// applyDefaults applies default values to the configuration.
func applyDefaults(name string, cfg Config) Config {
	if cfg.Region == "" {
		cfg.Region = defaultS3Region
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
		PresignEndpoint: cfg.PublicEndpoint,
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

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return "s3"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the name a call that names no connection binds: the
// default connection's bound name.
func (t *Toolkit) Connection() string {
	return t.name
}

// ListConnections reports every connection this toolkit serves, by bound
// name, for list_connections and the connection resolver.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	t.connMu.RLock()
	defer t.connMu.RUnlock()
	names := slices.Sorted(maps.Keys(t.connections))
	out := make([]toolkit.ConnectionDetail, 0, len(names))
	for _, name := range names {
		out = append(out, toolkit.ConnectionDetail{Name: name, Description: t.descriptions[name], IsDefault: name == t.name})
	}
	return out
}

// RegisterTools registers s3_list and s3_object with the MCP server. The
// platform's unified list_connections stands in for upstream's
// s3_list_connections, so nothing else from mcp-s3 is registered.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.s3Toolkit == nil {
		return
	}
	mcp.AddTool(s, t.tool(toolList, listTitle, listDescription, listAnnotations, listOutputSchema), t.handleList)
	mcp.AddTool(s, t.tool(toolObject, objectTitle, objectDescription, objectAnnotations, objectOutputSchema), t.handleObject)
}

// tool builds one registration, applying the instance's title, description and
// annotation overrides for that tool name when the configuration carries them.
func (t *Toolkit) tool(name, title, description string, annotations *mcp.ToolAnnotations, outputSchema any) *mcp.Tool {
	if v, ok := t.config.Titles[name]; ok {
		title = v
	}
	if v, ok := t.config.Descriptions[name]; ok {
		description = v
	}
	if v, ok := t.config.Annotations[name]; ok {
		annotations = toolkit.AnnotationsToMCP(v)
	}
	return &mcp.Tool{Name: name, Title: title, Description: description, Annotations: annotations, OutputSchema: outputSchema}
}

// Tools returns the tool names this toolkit registers. Both are registered
// whatever read_only says: s3_object carries the read actions too, and a
// writing action on a read-only connection is refused by the handler, naming
// the connection.
func (*Toolkit) Tools() []string {
	return []string{toolList, toolObject}
}

// SetSemanticProvider sets the semantic metadata provider for enrichment.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider sets the query execution provider for enrichment.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// AddConnection adds a named S3 connection at runtime.
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	cfg, err := ParseConfig(config)
	if err != nil {
		return fmt.Errorf("parsing S3 config for %s: %w", name, err)
	}
	// A connection added at run time is bound by the name it is stored under:
	// that is the name the admin API, the reconciler and every persona rule
	// already use for it.
	cfg = applyDefaults(name, cfg)
	cfg.ConnectionName = name
	if err := t.bind(cfg); err != nil {
		return fmt.Errorf("creating S3 client for %s: %w", name, err)
	}
	return nil
}

// RemoveConnection removes a named S3 connection at runtime.
func (t *Toolkit) RemoveConnection(name string) error {
	if err := t.s3Toolkit.RemoveClient(name); err != nil {
		return fmt.Errorf("removing S3 client %s: %w", name, err)
	}
	t.connMu.Lock()
	delete(t.connections, name)
	delete(t.descriptions, name)
	t.connMu.Unlock()
	return nil
}

// settings returns the settings a call that names connection is bound by. An
// empty name is the default connection. A name the toolkit resolves through
// the upstream client registry but never entered here (a client added on the
// upstream toolkit directly) is bound by the default's settings.
func (t *Toolkit) settings(connection string) connSettings {
	if connection == "" {
		connection = t.name
	}
	t.connMu.RLock()
	defer t.connMu.RUnlock()
	if s, ok := t.connections[connection]; ok {
		return s
	}
	return settingsOf(t.config)
}

// HasConnection returns true if a connection with the given name exists.
func (t *Toolkit) HasConnection(name string) bool {
	_, err := t.s3Toolkit.GetClient(name)
	return err == nil
}

// Close releases every connection's client.
func (t *Toolkit) Close() error {
	if t.s3Toolkit == nil {
		return nil
	}
	if err := t.s3Toolkit.Close(); err != nil {
		return fmt.Errorf("closing s3 clients: %w", err)
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
var (
	_ interface {
		Kind() string
		Name() string
		Connection() string
		RegisterTools(s *mcp.Server)
		Tools() []string
		SetSemanticProvider(provider semantic.Provider)
		SetQueryProvider(provider query.Provider)
		Close() error
	} = (*Toolkit)(nil)
	_ toolkit.ConnectionManager = (*Toolkit)(nil)
	_ toolkit.ConnectionLister  = (*Toolkit)(nil)
)
