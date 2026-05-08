package registry

import (
	"fmt"

	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	datahubkit "github.com/txn2/mcp-data-platform/pkg/toolkits/datahub"
	gatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/gateway"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// RegisterBuiltinFactories registers all built-in toolkit factories.
func RegisterBuiltinFactories(r *Registry) {
	r.RegisterAggregateFactory("trino", TrinoAggregateFactory)
	r.RegisterFactory("datahub", DataHubFactory)
	r.RegisterFactory("s3", S3Factory)
	r.RegisterAggregateFactory(gatewaykit.Kind, GatewayAggregateFactory)
	r.RegisterAggregateFactory(apigatewaykit.Kind, APIGatewayAggregateFactory)
}

// TrinoAggregateFactory creates a single multi-connection Trino toolkit
// from all configured instances. This ensures deterministic connection
// routing based on the "connection" parameter in each tool call, rather
// than the non-deterministic last-write-wins behavior of N separate toolkits.
func TrinoAggregateFactory(defaultName string, instances map[string]map[string]any) (Toolkit, error) {
	multiCfg, err := trinokit.ParseMultiConfig(defaultName, instances)
	if err != nil {
		return nil, fmt.Errorf("parsing trino multi config: %w", err)
	}
	tk, err := trinokit.NewMulti(multiCfg)
	if err != nil {
		return nil, fmt.Errorf("creating trino toolkit: %w", err)
	}
	return tk, nil
}

// TrinoFactory creates a Trino toolkit from configuration.
func TrinoFactory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := trinokit.ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing trino config: %w", err)
	}
	tk, err := trinokit.New(name, config)
	if err != nil {
		return nil, fmt.Errorf("creating trino toolkit: %w", err)
	}
	return tk, nil
}

// DataHubFactory creates a DataHub toolkit from configuration.
func DataHubFactory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := datahubkit.ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing datahub config: %w", err)
	}
	tk, err := datahubkit.New(name, config)
	if err != nil {
		return nil, fmt.Errorf("creating datahub toolkit: %w", err)
	}
	return tk, nil
}

// S3Factory creates an S3 toolkit from configuration.
func S3Factory(name string, cfg map[string]any) (Toolkit, error) {
	config, err := s3kit.ParseConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing s3 config: %w", err)
	}
	tk, err := s3kit.New(name, config)
	if err != nil {
		return nil, fmt.Errorf("creating s3 toolkit: %w", err)
	}
	return tk, nil
}

// GatewayAggregateFactory creates a multi-connection gateway toolkit from
// all configured instances. Per-instance config parse errors fail the
// factory; upstream connectivity failures are absorbed and logged so an
// unreachable upstream cannot block platform startup.
func GatewayAggregateFactory(defaultName string, instances map[string]map[string]any) (Toolkit, error) {
	cfg, err := gatewaykit.ParseMultiConfig(defaultName, instances)
	if err != nil {
		return nil, fmt.Errorf("parsing gateway multi config: %w", err)
	}
	return gatewaykit.NewMulti(cfg), nil
}

// APIGatewayAggregateFactory creates a multi-connection api-gateway
// toolkit from all configured instances. Per-instance config parse
// errors fail the factory; per-connection materialization failures
// (auth-builder errors) are logged and skipped so a single bad
// connection cannot block platform startup. Outbound HTTP failures
// happen at invocation time and are surfaced through the tool's
// response envelope, not at startup.
func APIGatewayAggregateFactory(defaultName string, instances map[string]map[string]any) (Toolkit, error) {
	cfg, err := apigatewaykit.ParseMultiConfig(defaultName, instances)
	if err != nil {
		return nil, fmt.Errorf("parsing apigateway multi config: %w", err)
	}
	return apigatewaykit.NewMulti(cfg), nil
}
