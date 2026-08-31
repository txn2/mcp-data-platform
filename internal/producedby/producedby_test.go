package producedby

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
