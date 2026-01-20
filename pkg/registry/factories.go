package registry

import (
	datahubkit "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// RegisterBuiltinFactories registers all built-in toolkit factories.
func RegisterBuiltinFactories(r *Registry) {
	r.RegisterFactory("trino", TrinoFactory)
	r.RegisterFactory("datahub", DataHubFactory)
	r.RegisterFactory("s3", S3Factory)
}

// TrinoFactory creates a Trino toolkit from configuration.
func TrinoFactory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := trinokit.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	return trinokit.New(name, config)
}

// DataHubFactory creates a DataHub toolkit from configuration.
func DataHubFactory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := datahubkit.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	return datahubkit.New(name, config)
}

// S3Factory creates an S3 toolkit from configuration.
func S3Factory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := s3kit.ParseConfig(cfg)
	if err != nil {
		return nil, err
	}
	return s3kit.New(name, config)
}
