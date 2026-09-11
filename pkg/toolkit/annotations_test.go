package toolkit

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnnotationsToMCP(t *testing.T) {
	tr, fa := true, false

	t.Run("all unset yields empty annotations", func(t *testing.T) {
		ann := AnnotationsToMCP(AnnotationConfig{})
		require.NotNil(t, ann)
		assert.False(t, ann.ReadOnlyHint)
		assert.Nil(t, ann.DestructiveHint)
		assert.False(t, ann.IdempotentHint)
		assert.Nil(t, ann.OpenWorldHint)
	})

	t.Run("set hints are applied", func(t *testing.T) {
		ann := AnnotationsToMCP(AnnotationConfig{
			ReadOnlyHint:    &tr,
			DestructiveHint: &fa,
			IdempotentHint:  &tr,
			OpenWorldHint:   &fa,
		})
		assert.True(t, ann.ReadOnlyHint)
		require.NotNil(t, ann.DestructiveHint)
		assert.False(t, *ann.DestructiveHint)
		assert.True(t, ann.IdempotentHint)
		require.NotNil(t, ann.OpenWorldHint)
		assert.False(t, *ann.OpenWorldHint)
	})
}

// TestReadOnlyAnnotationsWireShape pins what a read-only tool puts on the
// wire. The whole of #1692 is about what a client reads out of tools/list, and
// the SDK decides that: v1.6 tagged readOnlyHint omitempty, so an explicit
// false serialized to nothing and was indistinguishable from an unannotated
// tool. v1.7 emits it. This fails the day that changes back, which is the day
// every write tool here silently stops saying anything.
func TestReadOnlyAnnotationsWireShape(t *testing.T) {
	ann := ReadOnlyAnnotations()
	require.True(t, ann.ReadOnlyHint, "a read advertises readOnlyHint")
	require.True(t, ann.IdempotentHint, "a read advertises idempotentHint")
	require.Nil(t, ann.DestructiveHint, "destructiveHint is meaningful only on a write")

	raw, err := json.Marshal(&mcp.Tool{Name: "probe", Annotations: ann})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"readOnlyHint":true`)
}

// TestWriteAnnotationsWireShape pins the same for a write, in both destructive
// states. destructiveHint must be PRESENT on a purely additive write: the MCP
// default for an absent one is true, so omitting it says the opposite.
func TestWriteAnnotationsWireShape(t *testing.T) {
	for _, tc := range []struct {
		name        string
		destructive bool
		want        string
	}{
		{name: "destructive", destructive: true, want: `"destructiveHint":true`},
		{name: "additive", destructive: false, want: `"destructiveHint":false`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ann := WriteAnnotations(tc.destructive)
			require.False(t, ann.ReadOnlyHint, "a write does not advertise readOnlyHint")
			require.NotNil(t, ann.DestructiveHint, "a write states destructiveHint rather than inheriting it")
			require.Equal(t, tc.destructive, *ann.DestructiveHint)

			raw, err := json.Marshal(&mcp.Tool{Name: "probe", Annotations: ann})
			require.NoError(t, err)
			require.Contains(t, string(raw), `"readOnlyHint":false`)
			require.Contains(t, string(raw), tc.want)
		})
	}
}

// TestAnnotationConstructorsReturnDistinctValues covers the reason each
// constructor builds a fresh value: mcp.ToolAnnotations is mutable and every
// registration hands the SDK its own, so one tool's annotation cannot be
// changed by writing through another's.
func TestAnnotationConstructorsReturnDistinctValues(t *testing.T) {
	first, second := ReadOnlyAnnotations(), ReadOnlyAnnotations()
	require.NotSame(t, first, second)
	first.ReadOnlyHint = false
	require.True(t, second.ReadOnlyHint, "mutating one annotation must not reach another")

	a, b := WriteAnnotations(true), WriteAnnotations(true)
	require.NotSame(t, a, b)
	require.NotSame(t, a.DestructiveHint, b.DestructiveHint)
}
