// Package trino provides a Trino toolkit adapter for the MCP data platform.
package trino

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	"github.com/txn2/mcp-trino/pkg/multiserver"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
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

	// trinoSourceName identifies this client to Trino in the X-Trino-Source
	// header so server-side query logs can attribute traffic back to the
	// platform.
	trinoSourceName = "mcp-data-platform"

	// kindTrino is the toolkit kind identifier.
	kindTrino = "trino"

	// Trino tool names. The full set is named here even though only
	// a subset crosses goconst's literal-repetition threshold — keeping
	// the Tools() list uniformly constant-driven avoids a visually mixed
	// list of constants and bare strings. Naming matches the convention
	// used in sibling toolkit packages (datahub, s3): no kind prefix
	// since the package path already provides it.
	toolQuery         = "trino_query"
	toolExecute       = "trino_execute"
	toolExplain       = "trino_explain"
	toolBrowse        = "trino_browse"
	toolDescribeTable = "trino_describe_table"
)

// Config holds Trino toolkit configuration.
type Config struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	User         string        `yaml:"user"`
	Password     string        `yaml:"password"` // #nosec G117 -- Trino credential from admin YAML config
	Catalog      string        `yaml:"catalog"`
	Schema       string        `yaml:"schema"`
	SSL          bool          `yaml:"ssl"`
	SSLVerify    bool          `yaml:"ssl_verify"`
	Timeout      time.Duration `yaml:"timeout"`
	DefaultLimit int           `yaml:"default_limit"`
	MaxLimit     int           `yaml:"max_limit"`
	ReadOnly     bool          `yaml:"read_only"`
	// ConnectionName is accepted for compatibility and has no effect. The
	// platform identifies a Trino connection by its instance name, which is
	// what the manager routes on and what Connection() reports (#1396).
	ConnectionName string                      `yaml:"connection_name"`
	Description    string                      `yaml:"description"` // Human-readable description of this connection's purpose
	Titles         map[string]string           `yaml:"titles"`
	Descriptions   map[string]string           `yaml:"descriptions"`
	Annotations    map[string]AnnotationConfig `yaml:"annotations"`

	// ProgressEnabled enables progress notifications for query execution.
	// Injected by the platform from progress.enabled config.
	ProgressEnabled bool `yaml:"progress_enabled"`

	// Elicitation configures user confirmation for expensive operations.
	// Injected by the platform from elicitation config.
	Elicitation ElicitationConfig `yaml:"elicitation"`

	// Scratch names the catalog and schema table registrations are written
	// into on this connection. Unset means registration is unavailable here.
	Scratch ScratchConfig `yaml:"scratch"`
}

// ScratchConfig names where a table registration writes on a connection.
//
// It is a target, not a boundary: the platform's read_only flag is a
// statement-prefix denylist and nothing in this toolkit restricts a catalog or
// schema, so what keeps a registration off the warehouse is the Trino identity
// the connection authenticates as, not these two fields.
type ScratchConfig struct {
	Catalog string `yaml:"catalog"`
	Schema  string `yaml:"schema"`
}

// Configured reports whether this target names somewhere to write.
func (s ScratchConfig) Configured() bool {
	return s.Catalog != "" && s.Schema != ""
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
	manager      *multiserver.Manager // non-nil in multi-connection mode
	trinoToolkit *trinotools.Toolkit

	semanticProvider semantic.Provider
	queryProvider    query.Provider

	// elicitation holds the middleware so providers can be propagated after init.
	elicitation *ElicitationMiddleware

	// connMu guards connectionDescriptions, which AddConnection and
	// RemoveConnection mutate from an admin HTTP goroutine while
	// ListConnections reads it from a tool-call goroutine.
	connMu sync.RWMutex

	// connectionDescriptions maps connection name → description (multi-connection mode).
	connectionDescriptions map[string]string

	// readOnly holds the read-only interceptor so connections added or removed
	// at runtime keep their read_only setting enforced. Nil only when no
	// connection restricts writes.
	readOnly *ReadOnlyInterceptor

	// scratch maps connection name -> the catalog and schema table
	// registrations write into on it. Per-instance Config is discarded after
	// NewMulti, so the target is kept here the way read_only is, and
	// maintained by AddConnection and RemoveConnection. Guarded by connMu.
	scratch map[string]ScratchConfig

	// exportDeps holds portal dependencies for trino_export (nil = export disabled).
	exportDeps *ExportDeps
}

// New creates a new Trino toolkit.
func New(name string, cfg Config) (*Toolkit, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	warnInertConnectionName(name, cfg.ConnectionName)
	cfg = applyDefaults(name, cfg)

	client, err := createClient(cfg)
	if err != nil {
		return nil, err
	}

	t := &Toolkit{
		name:    name,
		config:  cfg,
		client:  client,
		scratch: buildScratchTargets(map[string]Config{name: cfg}),
	}

	// Create elicitation middleware before toolkit so it can be passed as an option.
	if cfg.Elicitation.Enabled {
		t.elicitation = &ElicitationMiddleware{
			client: client,
			config: cfg.Elicitation,
		}
	}

	// A single-connection toolkit routes every call to the one client whatever
	// the connection argument says, so read_only holds for all of them. The
	// interceptor is kept on the toolkit as well as handed to the tools so
	// Exec runs the same check the MCP path runs.
	if cfg.ReadOnly {
		t.readOnly = NewReadOnlyInterceptor()
	}
	t.trinoToolkit = createToolkit(client, cfg, t.elicitation, t.readOnly)

	return t, nil
}

// NewMulti creates a multi-connection Trino toolkit that routes requests
// to the correct backend based on the "connection" parameter in each tool call.
// This replaces the previous pattern of creating N separate single-client
// toolkits that would clobber each other's tool registrations.
func NewMulti(cfg MultiConfig) (*Toolkit, error) {
	if len(cfg.Instances) == 0 {
		return nil, errors.New("at least one trino instance is required")
	}

	// Resolve the default connection name.
	defaultName := cfg.DefaultConnection
	if defaultName == "" {
		// Pick the first instance alphabetically for determinism.
		for name := range cfg.Instances {
			if defaultName == "" || name < defaultName {
				defaultName = name
			}
		}
	}

	defaultCfg, ok := cfg.Instances[defaultName]
	if !ok {
		return nil, fmt.Errorf("default connection %q not found in instances", defaultName)
	}

	// Validate all instance configs.
	for name, instCfg := range cfg.Instances {
		if err := validateConfig(instCfg); err != nil {
			return nil, fmt.Errorf("instance %s: %w", name, err)
		}
		warnInertConnectionName(name, instCfg.ConnectionName)
	}

	// Build multiserver config from instance configs.
	msCfg := buildMultiserverConfig(defaultName, defaultCfg, cfg.Instances)

	mgr := multiserver.NewManager(msCfg)

	// Use the default instance config for toolkit-level settings.
	defaultCfg = applyDefaults(defaultName, defaultCfg)

	descs := make(map[string]string, len(cfg.Instances))
	for name, instCfg := range cfg.Instances {
		descs[name] = instCfg.Description
	}

	t := &Toolkit{
		name:                   defaultName,
		config:                 defaultCfg,
		manager:                mgr,
		connectionDescriptions: descs,
		readOnly:               buildReadOnlyInterceptor(defaultName, cfg.Instances),
		scratch:                buildScratchTargets(cfg.Instances),
	}

	connRequired := buildConnectionRequired(defaultName, cfg.Instances)
	opts := buildToolkitOptions(defaultCfg, nil, connRequired, t.readOnly) // elicitation not supported in multi-mode yet
	t.trinoToolkit = trinotools.NewToolkitWithManager(mgr, trinotools.Config{
		DefaultLimit: defaultCfg.DefaultLimit,
		MaxLimit:     defaultCfg.MaxLimit,
	}, opts...)

	return t, nil
}

// warnInertConnectionName reports a connection_name the platform cannot honor.
//
// Connections are deny-by-default, so a persona whose connections.allow lists a
// Trino instance's connection_name reaches nothing: the name a call binds is the
// instance name (#1396). Dropping the key silently turns that into a refusal
// with no stated cause, so it is named here with the value to list instead.
func warnInertConnectionName(instance, connectionName string) {
	if connectionName == "" || connectionName == instance {
		return
	}
	// Both values come from deployment configuration, which an admin API write
	// can reach, so they are stripped of control characters before they reach a
	// log field or the sentence built from them.
	safeInstance := logsan.SanitizeForLog(instance)
	safeName := logsan.SanitizeForLog(connectionName)
	slog.Warn("trino connection_name has no effect; the connection is named by its instance",
		"instance", safeInstance,
		"connection_name", safeName,
		"remedy", "list "+safeInstance+" wherever "+safeName+" is named, including persona connections rules",
	)
}

// buildConnectionRequired creates a ConnectionRequiredMiddleware when multiple
// instances are configured. Returns nil for single-instance deployments.
func buildConnectionRequired(defaultName string, instances map[string]Config) *ConnectionRequiredMiddleware {
	if len(instances) <= 1 {
		return nil
	}
	connDescs := make([]ConnectionDescription, 0, len(instances))
	for name, instCfg := range instances {
		connDescs = append(connDescs, ConnectionDescription{
			Name:        name,
			Description: instCfg.Description,
			IsDefault:   name == defaultName,
		})
	}
	return NewConnectionRequiredMiddleware(connDescs)
}

// buildReadOnlyInterceptor creates the per-connection read-only interceptor for
// a multi-connection toolkit. It is installed whether or not any instance sets
// read_only, because toolkit options are fixed at construction: a connection
// added later (AddConnection) can then be enforced without rebuilding the
// toolkit.
func buildReadOnlyInterceptor(defaultName string, instances map[string]Config) *ReadOnlyInterceptor {
	readOnly := make(map[string]bool, len(instances))
	for name, instCfg := range instances {
		readOnly[name] = instCfg.ReadOnly
	}
	return NewConnectionReadOnlyInterceptor(defaultName, readOnly)
}

// buildScratchTargets collects each instance's registration target. Only
// instances that name one get an entry, so a lookup that misses is the
// unconfigured answer rather than an empty target.
func buildScratchTargets(instances map[string]Config) map[string]ScratchConfig {
	targets := make(map[string]ScratchConfig, len(instances))
	for name, instCfg := range instances {
		if instCfg.Scratch.Configured() {
			targets[name] = instCfg.Scratch
		}
	}
	return targets
}

// buildMultiserverConfig constructs a multiserver.Config from instance configs.
func buildMultiserverConfig(
	defaultName string,
	defaultCfg Config,
	instances map[string]Config,
) multiserver.Config {
	defaultCfg = applyDefaults(defaultName, defaultCfg)
	primary := trinoclient.Config{
		Host:      defaultCfg.Host,
		Port:      defaultCfg.Port,
		User:      defaultCfg.User,
		Password:  defaultCfg.Password,
		Catalog:   defaultCfg.Catalog,
		Schema:    defaultCfg.Schema,
		SSL:       defaultCfg.SSL,
		SSLVerify: defaultCfg.SSLVerify,
		Timeout:   defaultCfg.Timeout,
		Source:    trinoSourceName,
	}

	connections := make(map[string]multiserver.ConnectionConfig, len(instances)-1)
	for name, instCfg := range instances {
		if name == defaultName {
			continue
		}
		cc := multiserver.ConnectionConfig{
			Host: instCfg.Host,
		}
		if instCfg.Port != 0 {
			cc.Port = instCfg.Port
		}
		if instCfg.User != "" {
			cc.User = instCfg.User
		}
		if instCfg.Password != "" {
			cc.Password = instCfg.Password
		}
		if instCfg.Catalog != "" {
			cc.Catalog = instCfg.Catalog
		}
		if instCfg.Schema != "" {
			cc.Schema = instCfg.Schema
		}
		if instCfg.SSL {
			ssl := true
			cc.SSL = &ssl
		}
		connections[name] = cc
	}

	return multiserver.Config{
		Default:     defaultName,
		Primary:     primary,
		Connections: connections,
	}
}

// buildToolkitOptions constructs toolkit options from config. readOnly is nil
// when no connection restricts writes.
func buildToolkitOptions(
	cfg Config,
	elicit *ElicitationMiddleware,
	connRequired *ConnectionRequiredMiddleware,
	readOnly *ReadOnlyInterceptor,
) []trinotools.ToolkitOption {
	var opts []trinotools.ToolkitOption

	// Always scrub internal topology (connector transport envelopes) from
	// upstream engine errors before they reach tool callers.
	opts = append(opts, trinotools.WithMiddleware(&ErrorSanitizerMiddleware{}))

	if readOnly != nil {
		opts = append(opts, trinotools.WithQueryInterceptor(readOnly))
		// A per-connection interceptor also runs as a middleware, to learn
		// which connection each call named. The unconditional one refuses
		// writes whatever the answer, so it does not pay for the hook.
		if readOnly.perConnection() {
			opts = append(opts, trinotools.WithMiddleware(readOnly))
		}
	}
	if len(cfg.Titles) > 0 {
		opts = append(opts, trinotools.WithTitles(toTrinoToolNames(cfg.Titles)))
	}
	if len(cfg.Descriptions) > 0 {
		opts = append(opts, trinotools.WithDescriptions(toTrinoToolNames(cfg.Descriptions)))
	}
	if len(cfg.Annotations) > 0 {
		opts = append(opts, trinotools.WithAnnotations(toTrinoAnnotations(cfg.Annotations)))
	}
	if connRequired != nil {
		opts = append(opts, trinotools.WithMiddleware(connRequired))
	}
	if cfg.ProgressEnabled {
		opts = append(opts, trinotools.WithMiddleware(&ProgressInjector{}))
	}
	if elicit != nil {
		opts = append(opts, trinotools.WithMiddleware(elicit))
	}

	return opts
}

// validateConfig validates the required configuration fields.
func validateConfig(cfg Config) error {
	if cfg.Host == "" {
		return errors.New("trino host is required")
	}
	if cfg.User == "" {
		return errors.New("trino user is required")
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
		Source:    trinoSourceName,
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
func createToolkit(
	client *trinoclient.Client,
	cfg Config,
	elicit *ElicitationMiddleware,
	readOnly *ReadOnlyInterceptor,
) *trinotools.Toolkit {
	opts := buildToolkitOptions(cfg, elicit, nil, readOnly)
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
		result[trinotools.ToolName(k)] = toolkit.AnnotationsToMCP(v)
	}
	return result
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return kindTrino
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the name a tool call binds when it names none: the
// identity audit records it under, a persona's connection rules match, and the
// connection source map is keyed by.
//
// That name is the instance name in both modes. The multi-connection manager
// routes by instance, and this toolkit reports its connections by instance
// through ListConnections, so config.ConnectionName is a label nothing else in
// the platform can reach: returning it named a connection no persona rule could
// match, no `connection` argument could carry, and no source-map lookup could
// resolve (#1396).
func (t *Toolkit) Connection() string {
	return t.name
}

// RegisterTools registers Trino tools with the MCP server.
// The platform provides a unified list_connections tool, so the per-toolkit
// trino_list_connections is excluded.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	if t.trinoToolkit != nil {
		t.trinoToolkit.Register(s,
			trinotools.ToolQuery,
			trinotools.ToolExecute,
			trinotools.ToolExplain,
			trinotools.ToolBrowse,
			trinotools.ToolDescribeTable,
		)
	}
	if t.exportDeps != nil {
		t.registerExportTool(s)
	}
}

// Tools returns the list of tool names that would be provided by this toolkit.
func (t *Toolkit) Tools() []string {
	tools := []string{
		toolQuery,
		toolExecute,
		toolExplain,
		toolBrowse,
		toolDescribeTable,
	}
	if t.exportDeps != nil {
		tools = append(tools, exportToolName)
	}
	return tools
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

// ListConnections returns details for all connections managed by this toolkit.
// Implements toolkit.ConnectionLister.
func (t *Toolkit) ListConnections() []toolkit.ConnectionDetail {
	if t.manager == nil {
		// Single-client mode: one connection, advertised under the name a call
		// binds it by, which is what Connection() reports.
		return []toolkit.ConnectionDetail{{
			Name:        t.name,
			Description: t.config.Description,
			IsDefault:   true,
		}}
	}

	infos := t.manager.ConnectionInfos()
	details := make([]toolkit.ConnectionDetail, len(infos))

	t.connMu.RLock()
	defer t.connMu.RUnlock()
	for i, info := range infos {
		details[i] = toolkit.ConnectionDetail{
			Name:        info.Name,
			Description: t.connectionDescriptions[info.Name],
			IsDefault:   info.IsDefault,
		}
	}
	return details
}

// AddConnection adds a named connection at runtime.
// Requires multi-connection mode (created via NewMulti).
func (t *Toolkit) AddConnection(name string, config map[string]any) error {
	if t.manager == nil {
		return errors.New("dynamic connections require multi-connection mode")
	}

	warnInertConnectionName(name, getString(config, "connection_name"))

	conn := multiserver.ConnectionConfig{
		Host:     getString(config, "host"),
		Port:     getInt(config, "port", 0),
		User:     getString(config, "user"),
		Password: getString(config, "password"),
		Catalog:  getString(config, "catalog"),
		Schema:   getString(config, "schema"),
	}
	if ssl, ok := config["ssl"].(bool); ok {
		conn.SSL = &ssl
	}

	if err := t.manager.AddConnection(name, conn); err != nil {
		return fmt.Errorf("adding trino connection %s: %w", name, err)
	}

	t.connMu.Lock()
	defer t.connMu.Unlock()

	// Keep the description map current for list_connections.
	if t.connectionDescriptions == nil {
		t.connectionDescriptions = make(map[string]string)
	}
	t.connectionDescriptions[name] = getString(config, "description")

	// Enforce this connection's read_only from its first call, not from the
	// next restart. Until this lands the interceptor holds no setting for the
	// name and refuses writes on it, so the window between the manager
	// accepting the connection and this line is closed rather than open.
	if t.readOnly != nil {
		t.readOnly.SetConnection(name, getBool(config, "read_only"))
	}

	// Same reasoning for the registration target: without this the connection
	// would register nothing until the next restart.
	if scratch := getScratchConfig(config); scratch.Configured() {
		if t.scratch == nil {
			t.scratch = make(map[string]ScratchConfig)
		}
		t.scratch[name] = scratch
	} else {
		delete(t.scratch, name)
	}

	return nil
}

// RemoveConnection removes a named connection at runtime.
// Requires multi-connection mode (created via NewMulti).
func (t *Toolkit) RemoveConnection(name string) error {
	if t.manager == nil {
		return errors.New("dynamic connections require multi-connection mode")
	}
	if err := t.manager.RemoveConnection(name); err != nil {
		return fmt.Errorf("removing trino connection %s: %w", name, err)
	}

	t.connMu.Lock()
	defer t.connMu.Unlock()
	delete(t.connectionDescriptions, name)
	delete(t.scratch, name)
	if t.readOnly != nil {
		t.readOnly.ForgetConnection(name)
	}
	return nil
}

// HasConnection returns true if a connection with the given name exists.
func (t *Toolkit) HasConnection(name string) bool {
	if t.manager == nil {
		return false
	}
	return t.manager.HasConnection(name)
}

// Close releases resources.
func (t *Toolkit) Close() error {
	if t.manager != nil {
		if err := t.manager.Close(); err != nil {
			return fmt.Errorf("closing trino manager: %w", err)
		}
		return nil
	}
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

// Manager returns the multi-connection manager when the toolkit was
// constructed in multi-mode (the typical platform startup path). Returns
// nil for single-connection toolkits. Callers that need a per-connection
// client (gateway enrichment, cross-toolkit lookups) use this to look up
// a client by name.
func (t *Toolkit) Manager() *multiserver.Manager {
	return t.manager
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
	_ toolkit.ConnectionLister  = (*Toolkit)(nil)
	_ toolkit.ConnectionManager = (*Toolkit)(nil)
)
