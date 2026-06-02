package platform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// TestWireAPIGatewayMemBudget_CreatesAndConfigures proves the platform
// creates the process-wide in-flight memory budget (issue #535) sized
// from config and injects it without error when an api toolkit is
// registered.
func TestWireAPIGatewayMemBudget_CreatesAndConfigures(t *testing.T) {
	mc, err := apigatewaykit.ParseMultiConfig("api", map[string]map[string]any{
		"c": {"base_url": "https://x.example.com"},
	})
	require.NoError(t, err)
	tk := apigatewaykit.NewMulti(mc)
	t.Cleanup(func() { _ = tk.Close() })

	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))

	p := &Platform{
		toolkitRegistry: reg,
		config: &Config{APIGateway: APIGatewayConfig{
			Memory: APIGatewayMemoryConfig{MaxInFlightBytes: 4096},
		}},
	}

	p.WireAPIGatewayMemBudget()

	require.NotNil(t, p.apiMemBudget)
	assert.True(t, p.apiMemBudget.Enabled())
	assert.Equal(t, int64(4096), p.apiMemBudget.Max())

	// Idempotent: a second call keeps the same budget instance so
	// toolkits registered later still share one ceiling.
	first := p.apiMemBudget
	p.WireAPIGatewayMemBudget()
	assert.Same(t, first, p.apiMemBudget)
}

// TestWireAPIGatewayMemBudget_DisabledByDefault proves a zero/unset
// max yields a disabled (unlimited) budget that is still safe to wire.
func TestWireAPIGatewayMemBudget_DisabledByDefault(t *testing.T) {
	p := &Platform{
		toolkitRegistry: registry.NewRegistry(),
		config:          &Config{},
	}
	p.WireAPIGatewayMemBudget()
	require.NotNil(t, p.apiMemBudget)
	assert.False(t, p.apiMemBudget.Enabled(), "unset max must be a disabled budget")
}

func TestAPIGatewayRawMaxBytes(t *testing.T) {
	p := &Platform{config: &Config{APIGateway: APIGatewayConfig{
		Memory: APIGatewayMemoryConfig{RawMaxBytes: 8192},
	}}}
	assert.Equal(t, int64(8192), p.APIGatewayRawMaxBytes())
}

// TestLoadConfig_APIGatewayMemory proves the new platform-level knobs
// parse from YAML so operators can configure them.
func TestLoadConfig_APIGatewayMemory(t *testing.T) {
	yaml := []byte(`
apigateway:
  memory:
    max_in_flight_bytes: 314572800
    raw_max_bytes: 1073741824
`)
	cfg, err := LoadConfigFromBytes(yaml)
	require.NoError(t, err)
	assert.Equal(t, int64(314572800), cfg.APIGateway.Memory.MaxInFlightBytes)
	assert.Equal(t, int64(1073741824), cfg.APIGateway.Memory.RawMaxBytes)
}
