package platform

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// piiColumns is a semantic provider that tags one column of one table PII.
type piiColumns struct{ semantic.Provider }

func (piiColumns) GetTableContext(context.Context, semantic.TableIdentifier) (*semantic.TableContext, error) {
	return &semantic.TableContext{}, nil
}

func (piiColumns) GetColumnsContext(_ context.Context, t semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	if t.String() == "hive.crm.customers" {
		return map[string]*semantic.ColumnContext{"email": {IsPII: true}}, nil
	}
	return map[string]*semantic.ColumnContext{}, nil
}

// TestConsentMiddleware_TheChainAsksBeforeATrinoQueryRuns is #2052 through
// the platform's own registration: the chain's consent entry installs the
// registered Trino toolkit's consent layer on the platform's server, and a
// trino_query reading a PII column is asked about and refused when declined,
// before anything is sent to Trino (the host here answers nothing).
func TestConsentMiddleware_TheChainAsksBeforeATrinoQueryRuns(t *testing.T) {
	tk, err := trinokit.NewMulti(trinokit.MultiConfig{DefaultConnection: "warehouse", Instances: map[string]trinokit.Config{"warehouse": {
		Host: "127.0.0.1", Port: 1, User: "u",
		Elicitation: trinokit.ElicitationConfig{Enabled: true, PIIConsent: trinokit.PIIConsentConfig{Enabled: true}},
	}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	tk.SetSemanticProvider(piiColumns{})
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))

	server := mcp.NewServer(&mcp.Implementation{Name: "p", Version: "v0"}, nil)
	tk.RegisterTools(server)
	p := &Platform{mcpServer: server, toolkitRegistry: reg}
	for _, spec := range p.receivingMiddlewareChain() {
		if spec.Name == mwConsent {
			spec.Register()
		}
	}

	var asked []string
	st, ct := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked = append(asked, req.Params.Message)
			return &mcp.ElicitResult{Action: "decline"}, nil
		},
	}).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "trino_query",
		Arguments: map[string]any{"sql": "SELECT email FROM hive.crm.customers"},
	})
	require.NoError(t, err)
	require.Len(t, asked, 1)
	assert.Contains(t, asked[0], "PII column")
	require.True(t, res.IsError)
	assert.Contains(t, firstTextOf(res), "PII access not authorized")
}

// firstTextOf is a result's first text block, "" when it has none.
func firstTextOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// TestNew_RefusesANegativeThumbnailSourceBound: the bounds are installed
// before anything else is built (#2072), and one below zero stops startup
// with the setting named.
func TestNew_RefusesANegativeThumbnailSourceBound(t *testing.T) {
	_, err := New(WithConfig(&Config{Thumbnails: ThumbnailsConfig{MaxSourceBytes: -1}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_source_bytes")
}
