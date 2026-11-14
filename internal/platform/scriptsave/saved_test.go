package scriptsave

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestSaved tells the version's handlers after a landed save, with who saved.
func TestSaved(t *testing.T) {
	var got []any
	g := &Gate{HandlersSaved: func(_ context.Context, sc *script.Script, c Caller) { got = append(got, sc.Name, c.Email, c.Admin) }}
	g.Saved(context.Background(), &script.Script{ID: "s1", Name: "guard"}, Caller{Email: "admin@example.com", Admin: true})
	assert.Equal(t, []any{"guard", "admin@example.com", true}, got)
	g.Saved(context.Background(), nil, Caller{})
	assert.Len(t, got, 3, "no script tells no one")
	(&Gate{}).Saved(context.Background(), &script.Script{Name: "x"}, Caller{})
}
