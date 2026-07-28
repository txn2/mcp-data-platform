package search

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// denyAllScope is a knowledge.ConnectionScope that grants nothing and maps every
// URN to one connection, so the whole catalog is withheld from any caller.
type denyAllScope struct{}

func (denyAllScope) AllowConnection(_, _ string) bool { return false }

func (denyAllScope) ConnectionsForURN(string) []string { return []string{"payroll"} }

// TestSearchOutput_WithheldNoticeReachesTheAgent proves the coverage block does
// not merely shorten: the tool reports how many results the caller's persona hid
// and what to do about it, through the real handler and router.
func TestSearchOutput_WithheldNoticeReachesTheAgent(t *testing.T) {
	router := knowledge.NewRouter(nil, nil, knowledge.NewCatalogProvider(globalCatalog{}))
	router.SetConnectionScope(denyAllScope{})
	tk := New("default", router)

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: userAID, UserEmail: userAEmail, PersonaName: "analyst",
	})
	out := callSearch(ctx, t, tk, "global")

	assert.Empty(t, hitsOf(out), "every catalog hit belongs to a connection the persona is denied")
	require.Len(t, out.Coverage, 1)
	assert.Equal(t, knowledge.SourceCatalog, out.Coverage[0].Source)
	assert.Equal(t, 0, out.Coverage[0].Matched)
	assert.Equal(t, 1, out.Coverage[0].Withheld)
	assert.Contains(t, out.WithheldNotice, "1 result is hidden")
	assert.Contains(t, out.WithheldNotice, "your persona (analyst)")
	assert.Contains(t, out.WithheldNotice, "Ask an administrator")
}

// TestSearchOutput_NoNoticeWhenNothingWithheld keeps the notice out of the
// ordinary response, so its presence is a signal rather than boilerplate.
func TestSearchOutput_NoNoticeWhenNothingWithheld(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(ctxFor(userAID, userAEmail), t, tk, "alice")
	assert.Empty(t, out.WithheldNotice)
	for _, c := range out.Coverage {
		assert.Zero(t, c.Withheld, "source %s", c.Source)
	}
}
