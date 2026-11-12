package configwarn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// fakeReacher is an S3 toolkit's connections and the buckets each lists.
type fakeReacher struct {
	buckets map[string][]string
	fail    map[string]bool
}

func (f fakeReacher) ListConnections() []connectionInfo {
	out := make([]connectionInfo, 0, len(f.buckets))
	for name := range f.buckets {
		out = append(out, connectionInfo{Name: name})
	}
	return out
}

func (f fakeReacher) BucketNames(_ context.Context, connection string) ([]string, error) {
	if f.fail[connection] {
		return nil, errors.New("AccessDenied")
	}
	return f.buckets[connection], nil
}

// TestCheckS3Reach is #2616's boot warning: a connection a persona may read
// buckets through whose credentials list the platform's bucket, or a backup
// bucket, is warned of and counted; a connection no persona reads buckets
// through, and one whose listing fails, is not.
func TestCheckS3Reach(t *testing.T) {
	s3kit.SetReservedBuckets(s3kit.PlatformBuckets("portal-assets", "managed-resources"))
	t.Cleanup(func() { s3kit.SetReservedBuckets(nil) })
	buf := captureLog(t)
	scrape := withMetrics(t)
	personas := newPersonas(t,
		&persona.Persona{
			Name: "analyst", Roles: []string{"analyst"}, Tools: persona.ToolRules{Allow: []string{"s3_*"}},
			Connections: persona.ConnectionRules{Allow: []string{"lake"}},
		},
		&persona.Persona{
			Name: "ops", Roles: []string{"ops"}, Tools: persona.ToolRules{Allow: []string{"trino_query"}},
			Connections: persona.ConnectionRules{Allow: []string{"*"}},
		},
	)
	r := fakeReacher{
		buckets: map[string][]string{
			"lake":     {"acme-lake", "managed-resources", "db-backups", "nightly.bak", "backlog"},
			"internal": {"portal-assets"},
			"denied":   {"portal-assets"},
		},
		fail: map[string]bool{"denied": true},
	}
	checkReacher(context.Background(), r, personas)

	log := buf.String()
	assert.Contains(t, log, "code="+CodeS3ReachesPlatformBucket)
	assert.Contains(t, log, "connection=lake")
	assert.Contains(t, log, "bucket=managed-resources")
	assert.Contains(t, log, "bucket=db-backups")
	assert.Contains(t, log, "bucket=nightly.bak")
	assert.NotContains(t, log, "bucket=backlog", "a word that only starts like one is not a backup")
	assert.NotContains(t, log, "bucket=acme-lake")
	assert.NotContains(t, log, "connection=internal", "no persona reads buckets through it")
	assert.NotContains(t, log, "connection=denied")
	m := scrape()
	assert.Contains(t, m, `code="`+CodeS3ReachesPlatformBucket+`"`)
	assert.Contains(t, m, `code="`+CodeS3ReachesBackupBucket+`"`)
	assert.Equal(t, 2, strings.Count(log, "code="+CodeS3ReachesBackupBucket))
}

// TestCheckS3Reach_NothingToRead: with no registry, no personas or no S3
// toolkit, the check reads nothing and warns of nothing.
func TestCheckS3Reach_NothingToRead(t *testing.T) {
	buf := captureLog(t)
	CheckS3Reach(context.Background(), nil, nil)
	CheckS3Reach(context.Background(), registry.NewRegistry(), newPersonas(t))
	assert.Empty(t, buf.String())
}

// TestCheckS3Reach_ReadsTheS3Toolkits: the check reads the registry's S3
// toolkits; a connection whose listing fails (nothing answers at its
// endpoint) is passed over without a warning.
func TestCheckS3Reach_ReadsTheS3Toolkits(t *testing.T) {
	buf := captureLog(t)
	tk, err := registry.S3AggregateFactory("lake", map[string]map[string]any{
		"lake": {"endpoint": "http://127.0.0.1:1", "region": "us-east-1", "use_path_style": true, "access_key_id": "k", "secret_access_key": "s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.NewRegistry()
	if err := reg.Register(tk); err != nil {
		t.Fatal(err)
	}
	personas := newPersonas(t, &persona.Persona{
		Name: "analyst", Roles: []string{"analyst"},
		Tools: persona.ToolRules{Allow: []string{"s3_list"}}, Connections: persona.ConnectionRules{Allow: []string{"lake"}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), s3ReachTimeout)
	defer cancel()
	CheckS3Reach(ctx, reg, personas)
	assert.NotContains(t, buf.String(), "code="+CodeS3ReachesPlatformBucket)
	s3tk, ok := tk.(*s3kit.Toolkit)
	require.True(t, ok)
	assert.Len(t, reacher{tk: s3tk}.ListConnections(), 1)
}
