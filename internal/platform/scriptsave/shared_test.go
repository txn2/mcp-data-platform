package scriptsave

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptshare"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type sharedSources struct {
	sources []scriptshare.Source
	err     error
}

func (s sharedSources) CodeSources(context.Context) ([]scriptshare.Source, error) {
	return s.sources, s.err
}

const helperSource = `def window(day):
    """The week before day."""
    end = day - 1
    start = end - 6
    return [start, end]

def main():
    """Runs."""
    return window(10)
`

// TestSharedCode is #2273 at the gate: a save reports each function another
// source defines, a script is not compared with its own saved version, and a
// gate that cannot read the other sources reports none rather than refusing.
func TestSharedCode(t *testing.T) {
	ctx := context.Background()
	other := scriptshare.Source{ID: "other", Name: "weekly", Code: helperSource}
	g := &Gate{Shared: sharedSources{sources: []scriptshare.Source{other}}}
	got := g.sharedCode(ctx, Request{Name: "daily"}, helperSource)
	require.Len(t, got, 1)
	assert.Equal(t, "window", got[0].Function)
	assert.Equal(t, []string{"weekly"}, got[0].Scripts)

	assert.Equal(t, []scriptshare.Match{}, g.sharedCode(ctx, Request{Existing: &script.Script{ID: "other"}}, helperSource),
		"a script's own saved version is not another source")
	failing := &Gate{Shared: sharedSources{err: errors.New("db down")}}
	assert.Equal(t, []scriptshare.Match{}, failing.sharedCode(ctx, Request{}, helperSource))
	assert.Equal(t, []scriptshare.Match{}, (&Gate{}).sharedCode(ctx, Request{}, helperSource))

	refused := (&Gate{}).Check(ctx, Request{Name: "d", Source: "def broken(:\n"})
	assert.Equal(t, []scriptshare.Match{}, refused.SharedCode, "a refused save reports [] , never null")
}
