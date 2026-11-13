package scripttoken

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A token a run minted is replaced wherever a later call carries it, in its
// arguments and in its answer, at any depth and inside a longer string, and
// the failure is passed on as it was (#2558).
func TestMinted_ReplacesEveryTokenAnyCallCarries(t *testing.T) {
	m := &Minted{}
	var got []map[string]any
	var gotErr error
	record := m.Recorder(func(_ string, args, out map[string]any, err error) {
		got = append(got, args, out)
		gotErr = err
	})

	before := map[string]any{"body": "no token yet"}
	record("appdb.execute", before, nil, nil)
	assert.Equal(t, before, got[0], "nothing is replaced before a token is minted")

	m.Add("tok-abc.123")
	m.Add("tok-def.456")
	m.Add("")
	failed := errors.New("upstream down")
	record("api_invoke_endpoint",
		map[string]any{"body": map[string]any{"text": "Open https://x/signin/tok-abc.123 soon", "both": []any{"tok-def.456", 7}}},
		map[string]any{"echo": "tok-abc.123"}, failed)
	assert.Equal(t, map[string]any{"body": map[string]any{
		"text": "Open https://x/signin/" + RecordedToken + " soon", "both": []any{RecordedToken, 7},
	}}, got[2])
	assert.Equal(t, map[string]any{"echo": RecordedToken}, got[3])
	assert.Same(t, failed, gotErr)

	untouched := map[string]any{"sql": "SELECT 1"}
	record("appdb.query", untouched, nil, nil)
	require.Len(t, got, 6)
	assert.Equal(t, untouched, got[4])
	assert.Nil(t, got[5])
}

// A nil Minted keeps nothing and Add on it does not panic: a run with no
// recording passes its Add.
func TestMinted_NilKeepsNothing(t *testing.T) {
	var m *Minted
	m.Add("tok")
	assert.Nil(t, m)
}

// Host.Minted is told the token a live mint issued, and not a verify's, a
// failed mint's or a test's.
func TestHost_TellsMintedTheTokenALiveMintIssued(t *testing.T) {
	var minted []string
	live := callerFunc(func(name string, _ map[string]any) (map[string]any, error) {
		if name == CapabilityMint {
			return map[string]any{"token": "tok-live"}, nil
		}
		return map[string]any{"ok": true}, nil
	})
	h := Host{Live: live, Minted: func(tok string) { minted = append(minted, tok) }}
	_, err := h.call(CapabilityMint, map[string]any{})
	require.NoError(t, err)
	_, err = h.call(CapabilityVerify, map[string]any{"token": "tok-live"})
	require.NoError(t, err)
	assert.Equal(t, []string{"tok-live"}, minted)

	h.Live = callerFunc(func(string, map[string]any) (map[string]any, error) { return nil, errors.New("no key") })
	_, err = h.call(CapabilityMint, map[string]any{})
	require.Error(t, err)
	assert.Equal(t, []string{"tok-live"}, minted)
}

// callerFunc answers a call with a function.
type callerFunc func(name string, args map[string]any) (map[string]any, error)

func (f callerFunc) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	return f(name, args)
}

// Recorded wraps a recorder in place, and leaves a missing one alone.
func TestRecorded_WrapsTheRecorderInPlace(t *testing.T) {
	var got map[string]any
	record := func(_ string, args, _ map[string]any, _ error) { got = args }
	m := Recorded(&record)
	require.NotNil(t, m)
	m.Add("tok-xyz")
	record("echo", map[string]any{"text": "a tok-xyz b"}, nil, nil)
	assert.Equal(t, map[string]any{"text": "a " + RecordedToken + " b"}, got)

	var none func(string, map[string]any, map[string]any, error)
	assert.Nil(t, Recorded(&none))
	assert.Nil(t, none)
	assert.Nil(t, Recorded(nil))
}
