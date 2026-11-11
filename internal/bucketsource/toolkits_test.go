package bucketsource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// FromToolkits reads the deployment's own rules: the persona connection
// filter and the S3 toolkit's bucket_prefix check. Neither needs the object
// store to answer, so the client is configured against an address nothing
// listens on.
func TestFromToolkits_TheDeploymentsOwnRules(t *testing.T) {
	tk, err := s3kit.New("assets", s3kit.Config{
		Region: "us-east-1", Endpoint: "http://127.0.0.1:1", AccessKeyID: "dev-access-key", SecretAccessKey: "dev-secret-key",
		UsePathStyle: true, BucketPrefix: "web-",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(tk))
	personas := persona.NewRegistry()
	reads := persona.ToolRules{Allow: []string{"*"}}
	require.NoError(t, personas.Register(&persona.Persona{Name: "web", Tools: reads, Connections: persona.ConnectionRules{Allow: []string{"assets"}}}))
	require.NoError(t, personas.Register(&persona.Persona{Name: "finance", Tools: reads, Connections: persona.ConnectionRules{Allow: []string{"ledger"}}}))
	// The two shapes a persona denied S3 reads takes: every tool but s3_*, the
	// pattern the persona docs recommend, and s3_list alone.
	require.NoError(t, personas.Register(&persona.Persona{
		Name: "no-s3", Tools: persona.ToolRules{Allow: []string{"*"}, Deny: []string{"s3_*"}},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}))
	require.NoError(t, personas.Register(&persona.Persona{
		Name: "lister", Tools: persona.ToolRules{Allow: []string{"s3_list"}},
		Connections: persona.ConnectionRules{Allow: []string{"*"}},
	}))
	a := FromToolkits(reg, personas)

	b, err := a.Open(withPersona("web"), Source{Connection: "assets", Bucket: "web-site", Prefix: "exports"})
	require.NoError(t, err)
	assert.Equal(t, "web-site", b.Source().Bucket)

	_, err = a.Open(withPersona("web"), Source{Connection: "assets", Bucket: "finance-books"})
	require.ErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), `limited to buckets whose name starts with "web-"`)
	assert.NotContains(t, err.Error(), "dev-secret-key")

	_, err = a.Open(withPersona("finance"), Source{Connection: "assets", Bucket: "web-site"})
	require.ErrorContains(t, err, "not one your persona may use")
	_, err = a.Open(withPersona("nobody"), Source{Connection: "assets", Bucket: "web-site"})
	require.ErrorContains(t, err, "not one your persona may use", "a persona the registry does not hold is refused")
	_, err = a.Open(withPersona("web"), Source{Connection: "ledger", Bucket: "web-site"})
	require.ErrorContains(t, err, "not one your persona may use")

	_, err = a.Open(withPersona("no-s3"), Source{Connection: "assets", Bucket: "web-site"})
	require.ErrorContains(t, err, "your persona may not use s3_list", "a persona denied s3_* reads no bucket through a publish or an extract")
	_, err = a.Open(withPersona("lister"), Source{Connection: "assets", Bucket: "web-site"})
	require.ErrorContains(t, err, "your persona may not use s3_object", "listing a bucket is not reading its objects")

	allowAll := &persona.Persona{Name: "admin", Tools: reads, Connections: persona.ConnectionRules{Allow: []string{"*"}}}
	require.NoError(t, personas.Register(allowAll))
	_, err = a.Open(withPersona("admin"), Source{Connection: "missing", Bucket: "web-site"})
	require.ErrorContains(t, err, `no S3 connection named "missing"`)
	_, err = FromToolkits(reg, personas).Open(context.Background(), Source{Connection: "assets", Bucket: "web-site"})
	require.ErrorContains(t, err, "not one your persona may use", "a caller with no identity is no persona")
}

// The tools a read stands in for are the S3 toolkit's own names.
func TestFromToolkits_ReadsTheS3ToolkitsOwnToolNames(t *testing.T) {
	tk, err := s3kit.New("assets", s3kit.Config{Region: "us-east-1", Endpoint: "http://127.0.0.1:1", UsePathStyle: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	for _, tool := range readTools {
		assert.Contains(t, tk.Tools(), tool)
	}
}
