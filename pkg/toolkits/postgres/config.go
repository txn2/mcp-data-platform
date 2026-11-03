// Package postgres provides the application database connection kind (appdb):
// an application's own PostgreSQL database, which the scripts and endpoints an
// application is built from read and write (#2400, #2428).
//
// It is not a data source and registers no agent tool. Trino is the
// platform's analytical engine and holds no transaction, row lock or enforced
// constraint; this kind is an application's operational store. The script host
// calls Query (one statement in a READ ONLY transaction) and Execute (one
// statement committed in a transaction of its own) directly, for
// platform.query and platform.execute, so an application's request path pays
// no tool call per statement.
//
// Parameters are bound natively by the driver (jackc/pgx/v5) and never rendered
// into the SQL text, and a PostgreSQL error's DETAIL, which can repeat a value,
// goes to the script alone (direct.go).
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apigwtls"
	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/secretref"
)

const (
	// Kind is the connection-instance kind discriminator. Operators see it in
	// the admin UI's connection picker.
	Kind = "appdb"

	// errPrefix names this kind in every message a refused save produces.
	errPrefix = "appdb"

	// DefaultPort is PostgreSQL's port.
	DefaultPort = 5432
	// maxPort is the highest TCP port.
	maxPort = 65535
	// DefaultMaxConns is the pool size per replica when none is configured.
	DefaultMaxConns = 5
	// MaxMaxConns is the largest pool a connection may ask for per replica.
	MaxMaxConns = 100
	// DefaultStatementTimeout bounds one statement when the caller names no
	// timeout of their own.
	DefaultStatementTimeout = 30 * time.Second
	// MaxStatementTimeout is the most a connection's statement timeout, or a
	// caller's timeout_seconds, may be.
	MaxStatementTimeout = 10 * time.Minute
	// DefaultMaxRows is the row cap of one result when none is configured.
	DefaultMaxRows = 1000
	// MaxMaxRows is the largest row cap a connection may set.
	MaxMaxRows = 100000
	// DefaultConnectTimeout bounds the dial and the TLS handshake.
	DefaultConnectTimeout = 10 * time.Second
	// DefaultPoolWait is how long a statement waits for a free connection
	// from its pool before it fails naming the pool (#2402).
	DefaultPoolWait = 5 * time.Second
	// MaxPoolWait is the most pool_wait_timeout may be.
	MaxPoolWait = time.Minute
	// DefaultTransactionIdle is how long a script's transaction may sit with
	// no statement before it is rolled back (#2402).
	DefaultTransactionIdle = 10 * time.Second
	// MaxTransactionIdle is the most transaction_idle_timeout may be.
	MaxTransactionIdle = 10 * time.Minute
	// DefaultLedgerSchema is the schema in the target database that holds
	// the ledger of applied migrations (#2404).
	DefaultLedgerSchema = "mcp_schema"
)

// The TLS modes a connection takes, as libpq names them. The default is
// verify-full: a database reached over a network the platform does not own is
// otherwise one DNS answer away from a stranger holding the password.
const (
	SSLModeDisable    = "disable"
	SSLModeRequire    = "require"
	SSLModeVerifyCA   = "verify-ca"
	SSLModeVerifyFull = "verify-full"
)

// Config keys a postgres connection is stored under. The TLS keys are the ones
// the HTTP-based kinds use (internal/apigwtls), so the client key is encrypted
// at rest by the key set pkg/platform/fieldcrypt already holds and redacted on
// read by the same rule.
const (
	cfgKeyHost             = "host"
	cfgKeyPort             = "port"
	cfgKeyDatabase         = "database"
	cfgKeyUser             = "user"
	cfgKeyPassword         = "password"
	cfgKeySSLMode          = "sslmode"
	cfgKeyCABundle         = "tls_ca_bundle_pem"
	cfgKeyClientCert       = "mtls_client_cert_pem"
	cfgKeyClientKey        = "mtls_client_key_pem"
	cfgKeyMaxConns         = "max_conns"
	cfgKeyStatementTimeout = "statement_timeout"
	cfgKeyMaxRows          = "max_rows"
	cfgKeyReadOnly         = "read_only"
	cfgKeyDescription      = "description"
	cfgKeyStatementCache   = "statement_cache"
	cfgKeyPoolWait         = "pool_wait_timeout"
	cfgKeyTransactionIdle  = "transaction_idle_timeout"
	cfgKeyApplyPersonas    = "schema_apply_personas"
	cfgKeyLedgerSchema     = "schema_ledger"
)

// Config is one PostgreSQL connection.
type Config struct {
	// ConnectionName is the name a tool's connection argument binds, set from
	// the instance name.
	ConnectionName string
	// Description is what list_connections and the admin UI show under the
	// name. Empty is shown as nothing.
	Description string

	Host     string
	Port     int
	Database string
	User     string
	// Password is the database user's password. It may be a
	// {{secret:<name>}} reference, which the platform fills before the
	// connection is parsed (internal/connsecretref).
	Password string

	SSLMode       string
	CABundlePEM   string
	ClientCertPEM string
	ClientKeyPEM  string

	// MaxConns is the pool size on each replica.
	MaxConns int
	// StatementTimeout is the default bound of one statement.
	StatementTimeout time.Duration
	// MaxRows caps the rows one result returns.
	MaxRows int
	// ReadOnly refuses platform.execute on this connection for every script.
	ReadOnly bool
	// StatementCache keeps each statement prepared on the physical
	// connection that first ran it, so it is described once rather than on
	// every call (#2402). Off for PgBouncer in transaction mode, which hands
	// a session's statements to whichever server connection is free.
	StatementCache bool
	// PoolWait is how long a statement waits for a free pooled connection
	// before it fails naming the pool.
	PoolWait time.Duration
	// TransactionIdle is how long a script's transaction may go without a
	// statement before it is rolled back.
	TransactionIdle time.Duration
	// SchemaApplyPersonas are the personas that may apply and revert this
	// connection's migrations (#2404). Empty is administrators only.
	SchemaApplyPersonas []string
	// LedgerSchema is the schema in the target database holding the ledger
	// of applied migrations.
	LedgerSchema string
}

// SecretKeys are the configuration keys a postgres connection may name a
// stored secret in as {{secret:<name>}}. The value is read as the connection's
// pool is built (internal/connsecretref lists the same keys).
var SecretKeys = []string{cfgKeyPassword}

// MultiConfig is every configured instance of the kind.
type MultiConfig struct {
	DefaultName string
	Instances   map[string]Config
}

// ParseMultiConfig parses every instance. An instance that does not parse is
// logged and skipped, so one misconfigured connection cannot block startup.
func ParseMultiConfig(defaultName string, raw map[string]map[string]any) (MultiConfig, error) {
	parsed := make(map[string]Config, len(raw))
	for name, r := range raw {
		c, err := parseFilled(name, r)
		if err != nil {
			slog.Warn("skipping invalid connection instance", "kind", Kind, "instance", name, "error", err)
			continue
		}
		c.ConnectionName = name
		parsed[name] = c
	}
	return MultiConfig{DefaultName: defaultName, Instances: parsed}, nil
}

// parseFilled fills the connection's stored-secret references, then parses it.
func parseFilled(name string, raw map[string]any) (Config, error) {
	filled, err := secretref.FillConfigKeys(context.Background(), name, raw, SecretKeys...)
	if err != nil {
		return Config{}, fmt.Errorf("postgres: %w", err)
	}
	return ParseConfig(filled)
}

// ParseConfig reads a Config from the map a connection is stored as, applies
// the defaults and validates it.
func ParseConfig(cfg map[string]any) (Config, error) {
	c := Config{
		Description:      cfgmap.String(cfg, cfgKeyDescription),
		Host:             strings.TrimSpace(cfgmap.String(cfg, cfgKeyHost)),
		Port:             int(cfgmap.Int64(cfg, cfgKeyPort, DefaultPort)),
		Database:         strings.TrimSpace(cfgmap.String(cfg, cfgKeyDatabase)),
		User:             strings.TrimSpace(cfgmap.String(cfg, cfgKeyUser)),
		Password:         cfgmap.String(cfg, cfgKeyPassword),
		SSLMode:          cfgmap.StringDefault(cfg, cfgKeySSLMode, SSLModeVerifyFull),
		CABundlePEM:      cfgmap.String(cfg, cfgKeyCABundle),
		ClientCertPEM:    cfgmap.String(cfg, cfgKeyClientCert),
		ClientKeyPEM:     cfgmap.String(cfg, cfgKeyClientKey),
		MaxConns:         int(cfgmap.Int64(cfg, cfgKeyMaxConns, DefaultMaxConns)),
		StatementTimeout: cfgmap.Duration(cfg, cfgKeyStatementTimeout, DefaultStatementTimeout),
		MaxRows:          int(cfgmap.Int64(cfg, cfgKeyMaxRows, DefaultMaxRows)),
		ReadOnly:         cfgmap.Bool(cfg, cfgKeyReadOnly),
		StatementCache:   cfgmap.BoolDefault(cfg, cfgKeyStatementCache, true),
		PoolWait:         cfgmap.Duration(cfg, cfgKeyPoolWait, DefaultPoolWait),
		TransactionIdle:  cfgmap.Duration(cfg, cfgKeyTransactionIdle, DefaultTransactionIdle),

		SchemaApplyPersonas: cfgmap.Strings(cfg, cfgKeyApplyPersonas),
		LedgerSchema:        cfgmap.StringDefault(cfg, cfgKeyLedgerSchema, DefaultLedgerSchema),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate refuses a configuration the toolkit could not open, or one whose
// bounds no operator intends.
func (c Config) Validate() error {
	for _, check := range []func() error{c.validateAddress, c.validateSSLMode, c.validateLimits, c.validateTLS} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateAddress() error {
	switch {
	case c.Host == "":
		return errors.New("postgres: host is required")
	case strings.ContainsAny(c.Host, " /?#@"):
		return fmt.Errorf("postgres: host %q is not a host name or address", c.Host)
	case c.Port < 1 || c.Port > maxPort:
		return fmt.Errorf("postgres: port %d is out of range", c.Port)
	case c.Database == "":
		return errors.New("postgres: database is required")
	case c.User == "":
		return errors.New("postgres: user is required")
	default:
		return nil
	}
}

func (c Config) validateSSLMode() error {
	switch c.SSLMode {
	case SSLModeDisable, SSLModeRequire, SSLModeVerifyCA, SSLModeVerifyFull:
		return nil
	default:
		return fmt.Errorf("postgres: invalid sslmode %q (want %s, %s, %s or %s)",
			c.SSLMode, SSLModeDisable, SSLModeRequire, SSLModeVerifyCA, SSLModeVerifyFull)
	}
}

func (c Config) validateLimits() error {
	switch {
	case c.MaxConns < 1 || c.MaxConns > MaxMaxConns:
		return fmt.Errorf("postgres: max_conns must be between 1 and %d", MaxMaxConns)
	case c.StatementTimeout <= 0 || c.StatementTimeout > MaxStatementTimeout:
		return fmt.Errorf("postgres: statement_timeout must be positive and at most %s", MaxStatementTimeout)
	case c.MaxRows < 1 || c.MaxRows > MaxMaxRows:
		return fmt.Errorf("postgres: max_rows must be between 1 and %d", MaxMaxRows)
	case c.PoolWait <= 0 || c.PoolWait > MaxPoolWait:
		return fmt.Errorf("postgres: pool_wait_timeout must be positive and at most %s", MaxPoolWait)
	case c.TransactionIdle <= 0 || c.TransactionIdle > MaxTransactionIdle:
		return fmt.Errorf("postgres: transaction_idle_timeout must be positive and at most %s", MaxTransactionIdle)
	case !ledgerSchemaName.MatchString(c.LedgerSchema):
		return fmt.Errorf("postgres: schema_ledger %q is not a lowercase identifier", c.LedgerSchema)
	default:
		return nil
	}
}

// ledgerSchemaName is what schema_ledger may be: an unquoted lowercase
// identifier, so it is written into the ledger's DDL without quoting rules.
var ledgerSchemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func (c Config) validateTLS() error {
	if c.SSLMode == SSLModeDisable && (c.CABundlePEM != "" || c.ClientCertPEM != "") {
		return errors.New("postgres: TLS material is set but sslmode is disable")
	}
	//nolint:wrapcheck // apigwtls builds the message in this kind's voice
	return apigwtls.Validate(c.tlsMaterial())
}

// tlsMaterial is the connection's TLS material in the form apigwtls judges.
func (c Config) tlsMaterial() apigwtls.Material {
	return apigwtls.Material{
		ClientCertPEM: c.ClientCertPEM,
		ClientKeyPEM:  c.ClientKeyPEM,
		CABundlePEM:   c.CABundlePEM,
		ErrPrefix:     errPrefix,
	}
}
