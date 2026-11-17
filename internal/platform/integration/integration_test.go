package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecs(t *testing.T) {
	s, ok := SpecOf(OpSiteRouting)
	require.True(t, ok)
	assert.Equal(t, "route(req)", s.Contract())
	assert.Equal(t, "hosts", s.ItemsField)
	assert.Equal(t, "route(req)", Contracts())
	_, ok = SpecOf("nope")
	assert.False(t, ok)
	assert.Len(t, Specs(), 1)
}

// TestCheck: the handler kind's check, and one operation's, refuse a source
// that does not declare the operation (#2683) or defines no function of the
// contract's name taking one parameter.
func TestCheck(t *testing.T) {
	require.NoError(t, Check(routeSource))
	require.ErrorContains(t, Check("def main():\n    pass\n"), "route(req)")
	require.ErrorContains(t, Check("def route(req):\n    return {}\n"), `INTEGRATION = "site_routing"`,
		"a route helper in a script declaring nothing serves no integration")
	require.ErrorContains(t, Check("INTEGRATION = \"site_routing\"\n\ndef route():\n    pass\n"), "route(req)")
	require.ErrorContains(t, Check("def ("), "does not parse")

	s, _ := SpecOf(OpSiteRouting)
	require.NoError(t, s.CheckFor(routeSource))
	require.ErrorContains(t, s.CheckFor("def main():\n    route(1)\n\ndef route(x):\n    return x\n"), `add INTEGRATION = "site_routing"`)
	require.ErrorContains(t, s.CheckFor("INTEGRATION = \"site_routing\"\n\ndef main():\n    pass\n"), "the site routing integration calls route(req)")
	require.ErrorContains(t, s.CheckFor("def ("), "does not parse")
	assert.True(t, s.Serves("def route(req):\n    return {}\n"), "a run of a script bound before #2683 is started on route(req) alone")
	assert.False(t, s.Serves("def main():\n    pass\n"))
}

// TestRequestMap: a request's lists are [], never null, its items under the
// operation's field.
func TestRequestMap(t *testing.T) {
	s, _ := SpecOf(OpSiteRouting)
	raw, err := json.Marshal(Request{Reason: ReasonTimer}.Map(s))
	require.NoError(t, err)
	assert.JSONEq(t, `{"reason":"timer","added":[],"removed":[],"hosts":[]}`, string(raw))

	call := RunCall(s, Request{
		Reason: ReasonChange, Added: []map[string]any{{"hostname": "a.example.com"}},
		Items: []map[string]any{{"hostname": "a.example.com"}, {"hostname": "b.example.com"}}, Removed: []map[string]any{{"hostname": "c.example.com"}},
	})
	got, req, err := FromRunCall(call)
	require.NoError(t, err)
	assert.Equal(t, OpSiteRouting, got.Name)
	items, removed := RequestKeys(got, req)
	assert.Equal(t, []string{"a.example.com", "b.example.com"}, items)
	assert.Equal(t, []string{"c.example.com"}, removed)

	fn, _, err := Entry(call)
	require.NoError(t, err)
	assert.Equal(t, "route", fn)

	_, _, err = FromRunCall(map[string]any{"integration": "nope"})
	require.ErrorContains(t, err, "registers")
	_, _, err = FromRunCall(map[string]any{"integration": OpSiteRouting})
	require.ErrorContains(t, err, "no request")
	_, _, err = Entry(map[string]any{})
	require.Error(t, err)
}

// TestReadResult: only a dict of the keys asked about, each ready or failed
// with a string message, is read; a key not asked about is ignored; the
// message is cleaned and bounded.
func TestReadResult(t *testing.T) {
	keys := []string{"a.example.com", "b.example.com"}
	got, err := ReadResult(map[string]any{
		"a.example.com":     map[string]any{"status": "ready", "message": "routed"},
		"b.example.com":     map[string]any{"status": "failed", "message": "<b>dns</b>\u202e\nnext line"},
		"other.example.com": map[string]any{"status": "ready"},
	}, keys)
	require.NoError(t, err)
	assert.Equal(t, ItemResult{Status: StatusReady, Message: "routed"}, got["a.example.com"])
	assert.Equal(t, StatusFailed, got["b.example.com"].Status)
	assert.NotContains(t, got["b.example.com"].Message, "\u202e")
	assert.NotContains(t, got["b.example.com"].Message, "\n")
	assert.Contains(t, got["b.example.com"].Message, "<b>dns</b>", "kept as text; the page renders it as text")
	assert.NotContains(t, got, "other.example.com", "a key the request did not name is ignored")

	got, err = ReadResult(map[string]any{"a.example.com": map[string]any{"status": "ready", "message": nil}}, keys)
	require.NoError(t, err)
	assert.Empty(t, got["a.example.com"].Message)

	long := strings.Repeat("x", 900)
	got, err = ReadResult(map[string]any{"a.example.com": map[string]any{"status": "ready", "message": long}}, keys)
	require.NoError(t, err)
	assert.Len(t, []rune(got["a.example.com"].Message), maxMessage)

	for name, tc := range map[string]struct {
		value any
		want  string
	}{
		"not a dict":      {[]any{}, "a list"},
		"none":            {nil, "None"},
		"value not dict":  {map[string]any{"a.example.com": "ready"}, "a string"},
		"bad status":      {map[string]any{"a.example.com": map[string]any{"status": "ok"}}, `"ok"`},
		"message not str": {map[string]any{"a.example.com": map[string]any{"status": "ready", "message": 3.0}}, "a number"},
		"bool value":      {map[string]any{"a.example.com": true}, "a bool"},
		"dict message":    {map[string]any{"a.example.com": map[string]any{"status": "ready", "message": map[string]any{}}}, "a dict"},
		"other type":      {map[string]any{"a.example.com": map[string]any{"status": "ready", "message": struct{}{}}}, "struct"},
	} {
		_, err := ReadResult(tc.value, keys)
		require.ErrorContains(t, err, tc.want, name)
	}
}

// TestReturned: the worker's check of what route(req) returned, kept as the
// run's result; a wrong shape fails the run naming the contract.
func TestReturned(t *testing.T) {
	s, _ := SpecOf(OpSiteRouting)
	call := RunCall(s, Request{Reason: ReasonTimer, Items: []map[string]any{{"hostname": "a.example.com"}}})
	raw, err := Returned(call, map[string]any{"a.example.com": map[string]any{"status": "ready", "message": "ok"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a.example.com":{"status":"ready","message":"ok"}}`, string(raw))

	_, err = Returned(call, "nope")
	require.ErrorContains(t, err, "route(req)")
	_, err = Returned(map[string]any{}, map[string]any{})
	require.Error(t, err)

	got, err := readStored(raw, []string{"a.example.com"})
	require.NoError(t, err)
	assert.Equal(t, StatusReady, got["a.example.com"].Status)
	_, err = readStored(nil, nil)
	require.ErrorContains(t, err, "nothing")
	_, err = readStored([]byte("{"), nil)
	require.ErrorContains(t, err, "decoding")
}

func TestDiff(t *testing.T) {
	now := []Item{{Key: "b"}, {Key: "a"}, {Key: "d"}}
	stored := []StoredItem{{Key: "a"}, {Key: "c"}, {Key: "d", Removing: true}, {Key: "e", Removing: true}}
	added, removed := diff(now, stored)
	keys := func(items []Item) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Key)
		}
		return out
	}
	assert.Equal(t, []string{"b", "d"}, keys(added), "an item whose removal is in flight and is back is added again")
	require.Len(t, removed, 1)
	assert.Equal(t, "c", removed[0].Key)
}
