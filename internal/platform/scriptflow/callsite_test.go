package scriptflow

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptcallsite"
)

// recorder is a Caller and an Exporter that keeps the call site each call
// carried, as the audit middleware and the output writer would record it.
type recorder struct {
	mu    sync.Mutex
	sites []string
}

func (r *recorder) note(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sites = append(r.sites, strings.Join(scriptcallsite.From(ctx), ">"))
}

func (r *recorder) CallTool(ctx context.Context, _ string, _ map[string]any) (map[string]any, error) {
	r.note(ctx)
	return map[string]any{"rows": []any{}, "columns": []any{}, "row_count": 0}, nil
}

func (r *recorder) Export(ctx context.Context, _ scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	r.note(ctx)
	return &scriptrun.ExportResult{}, nil
}

func (r *recorder) PublishData(ctx context.Context, _ scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	r.note(ctx)
	return &scriptrun.ExportResult{}, nil
}

// The call site a real run records for each call is the call site the graph
// records on the card that makes it (#1907): the join the run overlay draws
// with, proved against the interpreter rather than assumed. It holds for a
// script whose top level calls main() and for one whose main() the platform
// calls (#1944), where the run has no module frame and the graph adds no site.
func TestCallSite_TheRunAndTheGraphAgree(t *testing.T) {
	const body = `
def fetch(path):
    return platform.call("api_invoke_endpoint", {"connection": "crm", "method": "GET", "path": path})

def stage(day):
    rows = platform.query("SELECT 1", connection="warehouse")
    fetch("/a")
    fetch("/b")
    platform.export("out-" + day, rows["rows"], format="csv")

def main():
    for day in ["d1", "d2"]:
        stage(day)
    platform.query("SELECT 2")
`
	for name, src := range map[string]string{
		"the top level calls main": body + "\nmain()\n",
		"the platform calls main":  body,
	} {
		t.Run(name, func(t *testing.T) { assertRunMatchesGraph(t, src) })
	}
}

func assertRunMatchesGraph(t *testing.T, src string) {
	t.Helper()
	rec := &recorder{}
	_, err := scriptrun.Run(context.Background(), scriptrun.Options{
		// The name a run executes under is the script's own.
		Source: src, Name: "nightly-orders", Caller: rec, Exporter: rec,
	})
	require.NoError(t, err)

	g := deriveGraph(src)
	require.True(t, g.OK, "%+v", g.Findings)
	cards := map[string]string{}
	for _, n := range g.Nodes {
		cards[strings.Join(n.CallSite, ">")] = n.Title + " " + n.Subtitle
	}
	require.Len(t, rec.sites, 9, "two passes of the loop, four calls each, and the last query")
	for _, site := range rec.sites {
		assert.Contains(t, cards, site, "a call the run made at %s has no card", site)
	}
	// The two helper calls are two cards, and each run of the loop lands on
	// the same cards.
	assert.Equal(t, rec.sites[:4], rec.sites[4:8])
	assert.NotEqual(t, rec.sites[1], rec.sites[2])
	assert.Equal(t, "API crm GET /a", cards[rec.sites[1]])
	assert.Equal(t, "API crm GET /b", cards[rec.sites[2]])
}
