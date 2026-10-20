package trinowiring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/registry"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// source is a registry holding the given toolkits.
type source []registry.Toolkit

func (s source) All() []registry.Toolkit { return s }

// otherKind is a registered toolkit that is not Trino's.
type otherKind struct{ registry.Toolkit }

// savedLake is a connection store holding one Trino connection with a
// scratch target.
type savedLake struct{}

func (savedLake) GetConnection(_ context.Context, name string) (map[string]any, error) {
	if name != "lake" {
		return nil, trinokit.ErrConnectionNotFound
	}
	return map[string]any{"host": "trino.example.com", "user": "u", "scratch": map[string]any{"catalog": "scratch", "schema": "uploads"}}, nil
}

func newTrino(t *testing.T) *trinokit.Toolkit {
	t.Helper()
	tk, err := trinokit.NewMulti(trinokit.MultiConfig{
		DefaultConnection: "warehouse",
		Instances:         map[string]trinokit.Config{"warehouse": {Host: "trino.example.com", User: "u"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

func TestToolkitsAndFirstReadOnlyTrino(t *testing.T) {
	a, b := newTrino(t), newTrino(t)
	reg := source{otherKind{}, a, otherKind{}, b}
	assert.Equal(t, []*trinokit.Toolkit{a, b}, Toolkits(reg))
	assert.Same(t, a, First(reg))
	assert.Nil(t, First(source{otherKind{}}))
	assert.Nil(t, Toolkits(nil))
	assert.Nil(t, First(nil))
}

// The store attached is the one a toolkit reads a connection another replica
// saved from (#2218); no store attaches nothing.
func TestConnectionStoreReachesEveryTrinoToolkit(t *testing.T) {
	a, b := newTrino(t), newTrino(t)
	reg := source{a, otherKind{}, b}

	ConnectionStore(reg, nil)
	_, ok := a.ScratchTarget("lake")
	assert.False(t, ok, "no store attached")

	ConnectionStore(reg, savedLake{})
	for _, tk := range []*trinokit.Toolkit{a, b} {
		target, ok := tk.ScratchTarget("lake")
		require.True(t, ok)
		assert.Equal(t, "uploads", target.Schema)
	}
}
