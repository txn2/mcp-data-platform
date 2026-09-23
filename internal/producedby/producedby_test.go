package producedby

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestProducerValid(t *testing.T) {
	cases := []struct {
		name string
		p    Producer
		want bool
	}{
		{"script", Producer{Kind: KindScript, ID: "s1"}, true},
		{"session", Producer{Kind: KindSession, ID: "sess-1"}, true},
		{"person", Producer{Kind: KindPerson, ID: "sub-1"}, true},
		{"no id", Producer{Kind: KindScript}, false},
		{"no kind", Producer{ID: "s1"}, false},
		{"unknown kind", Producer{Kind: "robot", ID: "s1"}, false},
		{"zero", Producer{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.p.Valid())
		})
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.False(t, Has(ctx), "an unstamped context names no producer")
	_, ok := From(ctx)
	assert.False(t, ok)

	p := Producer{Kind: KindScript, ID: "script-1", Label: "daily-sales"}
	ctx = With(ctx, p)
	got, ok := From(ctx)
	require.True(t, ok)
	assert.Equal(t, p, got)
	assert.True(t, Has(ctx))
}

// TestWithRefusesInvalid pins the rule that keeps an inner surface from
// blanking an outer one: a producer that names nothing is not stamped, so the
// script a run stamped survives a middleware that could not name a caller.
func TestWithRefusesInvalid(t *testing.T) {
	outer := With(context.Background(), Producer{Kind: KindScript, ID: "script-1"})
	inner := With(outer, Producer{Kind: KindSession, ID: ""})
	got, ok := From(inner)
	require.True(t, ok)
	assert.Equal(t, "script-1", got.ID, "the invalid stamp must not shadow the script")

	assert.False(t, Has(With(context.Background(), Producer{})))
}

// TestRunOutputKey is set for a script run's call alone, and keys the name the
// way platform.export does (#1854).
func TestRunOutputKey(t *testing.T) {
	ctx := With(context.Background(), Producer{Kind: KindScript, ID: "s1", Label: "daily"})
	key := RunOutputKey(ctx)
	if key == nil || key("report") != script.OutputIdentityKey("s1", "report") {
		t.Fatal("RunOutputKey for a script run is not the platform.export identity")
	}
	for _, ctx := range []context.Context{
		context.Background(),
		With(context.Background(), Producer{Kind: KindSession, ID: "sess"}),
	} {
		if RunOutputKey(ctx) != nil {
			t.Error("a call no script run made has no run output key")
		}
	}
}
