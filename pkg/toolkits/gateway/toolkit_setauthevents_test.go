package gateway

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// TestToolkitSetAuthEvents exercises the gateway toolkit's
// SetAuthEvents loop. The toolkit walks live connections to forward
// the writer; with an empty connection map this just sets the field.
func TestToolkitSetAuthEvents(t *testing.T) {
	t.Parallel()
	tk := New("test")
	writer := authevents.NewWriter(authevents.NewMemoryStore(), nil)
	tk.SetAuthEvents(writer)
	if tk.authEvents != writer {
		t.Error("SetAuthEvents should set the field")
	}
}
