package runpage

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

type counter struct {
	n   int
	err error
}

func (c counter) CountRuns(context.Context, script.RunFilter) (int, error) { return c.n, c.err }

func TestFilter_ReadsAPageOfOneScriptsRuns(t *testing.T) {
	q := url.Values{"status": {"failed"}, "live": {"true"}, "per_page": {"10"}, "page": {"3"}}
	assert.Equal(t, script.RunFilter{ScriptID: "s1", Status: "failed", Live: true, Limit: 10, Offset: 20}, Filter("s1", q, 25))
	assert.Equal(t, script.RunFilter{ScriptID: "s1", Limit: 25}, Filter("s1", url.Values{}, 25))
}

func TestTotal_IsTheCountOrWhatThePageKnows(t *testing.T) {
	f := script.RunFilter{Offset: 50}
	assert.Equal(t, 312, Total(context.Background(), counter{n: 312}, f, 25))
	assert.Equal(t, 75, Total(context.Background(), counter{err: errors.New("boom")}, f, 25))
	assert.Equal(t, 75, Total(context.Background(), struct{}{}, f, 25))
}
