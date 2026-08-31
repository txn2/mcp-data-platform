package middleware

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// TestStampProducerNamesTheSession is acceptance criterion 5 at the seam that
// decides it: an ordinary tool call files its writes under the session, which
// is the unit a reader can open.
func TestStampProducerNamesTheSession(t *testing.T) {
	pc := &PlatformContext{SessionID: "sess-1", UserID: "sub-1", UserEmail: "a@example.com"}
	got, ok := producedby.From(stampProducer(context.Background(), pc))
	require.True(t, ok)
	assert.Equal(t, producedby.KindSession, got.Kind)
	assert.Equal(t, "sess-1", got.ID)
}

// TestStampProducerFallsBackToTheCaller covers a call with no session at all.
func TestStampProducerFallsBackToTheCaller(t *testing.T) {
	pc := &PlatformContext{UserID: "sub-1", UserEmail: "a@example.com"}
	got, ok := producedby.From(stampProducer(context.Background(), pc))
	require.True(t, ok)
	assert.Equal(t, producedby.KindPerson, got.Kind)
	assert.Equal(t, "sub-1", got.ID)
	assert.Equal(t, "a@example.com", got.Label)
}

// TestStampProducerLeavesAScriptRunAlone is the rule that makes a script
// producer survive a rename: the run stamped its script id before this
// middleware ran, and this middleware knows only the script:<name> principal.
func TestStampProducerLeavesAScriptRunAlone(t *testing.T) {
	run := producedby.With(context.Background(), producedby.Producer{
		Kind: producedby.KindScript, ID: "script-1", Label: "daily-sales",
	})
	pc := &PlatformContext{SessionID: "run-abc", UserID: "script:daily-sales", AuthType: AuthTypeScript}
	got, ok := producedby.From(stampProducer(run, pc))
	require.True(t, ok)
	assert.Equal(t, producedby.KindScript, got.Kind)
	assert.Equal(t, "script-1", got.ID)
}

func TestStampProducerWithoutAPlatformContext(t *testing.T) {
	assert.False(t, producedby.Has(stampProducer(context.Background(), nil)))
}

// TestStampProducerNamesNobody covers a caller the platform could not identify
// at all: nothing is stamped, so nothing is recorded under a blank producer.
func TestStampProducerNamesNobody(t *testing.T) {
	assert.False(t, producedby.Has(stampProducer(context.Background(), &PlatformContext{})))
}
