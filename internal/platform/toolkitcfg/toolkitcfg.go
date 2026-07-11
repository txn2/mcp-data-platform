// Package toolkitcfg resolves typed per-toolkit connection configuration out of
// the platform's raw toolkits config (map[string]any decoded from YAML/JSON).
//
// The platform stores toolkit config as
// toolkits.<kind>.instances.<name>.<key>; these helpers walk that structure,
// pick the default instance when none is named, and extract the typed DataHub /
// Trino / S3 settings the providers need. Split out of pkg/platform to keep
// that package under its size budget (#756); the primitive typed-map accessors
// live in the sibling cfgmap package.
package toolkitcfg

import (
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/cfgmap"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

// Toolkit kind keys as they appear under the toolkits config map.
const (
	kindDataHub = "datahub"
	kindTrino   = "trino"
	kindS3      = "s3"
)

// Config schema keys within a toolkit kind's config map.
const (
	keyInstances = "instances"
	keyDefault   = "default"
)

// Trino connection defaults, applied when an instance omits the setting.
const (
	DefaultTrinoPort       = 8080
	DefaultTrinoQueryLimit = 1000
	DefaultTrinoMaxLimit   = 10000
)

// DataHub holds extracted DataHub configuration.
type DataHub struct {
	URL     string
	Token   string
	Timeout time.Duration
	Debug   bool
}

// Trino holds extracted Trino configuration.
type Trino struct {
	Host           string
	Port           int
	User           string
	Password       string // #nosec G117 -- Trino connection credential from admin config
	Catalog        string
	Schema         string
	SSL            bool
	SSLVerify      bool
	Timeout        time.Duration
	DefaultLimit   int
	MaxLimit       int
	ReadOnly       bool
	ConnectionName string
}

// S3 holds extracted S3 configuration.
type S3 struct {
	Region         string
	Endpoint       string
	AccessKeyID    string
	SecretKey      string
	BucketPrefix   string
	ConnectionName string
	UsePathStyle   bool
}

// InstanceConfig retrieves one instance's config map for a toolkit kind. When
// instance is "" it resolves the default (or first) instance. Returns nil if
// the kind, its instances map, or the named instance is absent or malformed.
func InstanceConfig(toolkits map[string]any, kind, instance string) map[string]any {
	toolkitsCfg, ok := toolkits[kind]
	if !ok {
		return nil
	}

	kindCfg, ok := toolkitsCfg.(map[string]any)
	if !ok {
		return nil
	}

	instances, ok := kindCfg[keyInstances].(map[string]any)
	if !ok {
		return nil
	}

	// If no instance name specified, try to get the default
	if instance == "" {
		instance = ResolveDefaultInstance(kindCfg, instances)
	}

	instanceCfg, ok := instances[instance].(map[string]any)
	if !ok {
		return nil
	}

	return instanceCfg
}

// ResolveDefaultInstance determines which instance to use: the one named by the
// kind's "default" key, else the first instance, else "".
func ResolveDefaultInstance(kindCfg, instances map[string]any) string {
	if defaultName, ok := kindCfg[keyDefault].(string); ok {
		return defaultName
	}
	// Use the first instance
	for name := range instances {
		return name
	}
	return ""
}

// DataHubConfig extracts DataHub configuration for the named instance (or the
// default instance when instance is ""). Returns nil if not configured.
func DataHubConfig(toolkits map[string]any, instance string) *DataHub {
	instanceCfg := InstanceConfig(toolkits, kindDataHub, instance)
	if instanceCfg == nil {
		return nil
	}

	cfg := &DataHub{
		URL:     cfgmap.String(instanceCfg, "url"),
		Token:   cfgmap.String(instanceCfg, fieldcrypt.CfgKeyToken),
		Timeout: cfgmap.Duration(instanceCfg, "timeout", 30*time.Second),
		Debug:   cfgmap.BoolDefault(instanceCfg, "debug", false),
	}

	// Support both "url" and "endpoint" keys
	if cfg.URL == "" {
		cfg.URL = cfgmap.String(instanceCfg, "endpoint")
	}

	return cfg
}

// TrinoConfig extracts Trino configuration for the named instance (or the
// default instance when instance is ""). Returns nil if not configured.
func TrinoConfig(toolkits map[string]any, instance string) *Trino {
	instanceCfg := InstanceConfig(toolkits, kindTrino, instance)
	if instanceCfg == nil {
		return nil
	}

	return &Trino{
		Host:           cfgmap.String(instanceCfg, "host"),
		Port:           cfgmap.Int(instanceCfg, "port", DefaultTrinoPort),
		User:           cfgmap.String(instanceCfg, "user"),
		Password:       cfgmap.String(instanceCfg, fieldcrypt.CfgKeyPassword),
		Catalog:        cfgmap.String(instanceCfg, "catalog"),
		Schema:         cfgmap.String(instanceCfg, "schema"),
		SSL:            cfgmap.Bool(instanceCfg, "ssl"),
		SSLVerify:      cfgmap.BoolDefault(instanceCfg, "ssl_verify", true),
		Timeout:        cfgmap.Duration(instanceCfg, "timeout", 120*time.Second),
		DefaultLimit:   cfgmap.Int(instanceCfg, "default_limit", DefaultTrinoQueryLimit),
		MaxLimit:       cfgmap.Int(instanceCfg, "max_limit", DefaultTrinoMaxLimit),
		ReadOnly:       cfgmap.Bool(instanceCfg, "read_only"),
		ConnectionName: cfgmap.String(instanceCfg, "connection_name"),
	}
}

// S3Config extracts S3 configuration for the named instance (or the default
// instance when instance is ""). Returns nil if not configured. When the
// instance omits connection_name it defaults to the instance name.
func S3Config(toolkits map[string]any, instance string) *S3 {
	instanceCfg := InstanceConfig(toolkits, kindS3, instance)
	if instanceCfg == nil {
		return nil
	}

	cfg := &S3{
		Region:         cfgmap.String(instanceCfg, "region"),
		Endpoint:       cfgmap.String(instanceCfg, "endpoint"),
		AccessKeyID:    cfgmap.String(instanceCfg, "access_key_id"),
		SecretKey:      cfgmap.String(instanceCfg, fieldcrypt.CfgKeySecretAccessKey),
		BucketPrefix:   cfgmap.String(instanceCfg, "bucket_prefix"),
		ConnectionName: cfgmap.String(instanceCfg, "connection_name"),
		UsePathStyle:   cfgmap.Bool(instanceCfg, "use_path_style"),
	}

	if cfg.ConnectionName == "" {
		cfg.ConnectionName = instance
	}

	return cfg
}
