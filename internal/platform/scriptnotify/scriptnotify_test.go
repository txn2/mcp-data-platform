package scriptnotify

import (
	"reflect"
	"strings"
	"testing"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// call evaluates a script calling notify or publish and returns what the
// binding read, or its error.
func call(t *testing.T, src string) (map[string]any, error) {
	t.Helper()
	var (
		got    map[string]any
		gotErr error
	)
	record := func(read func(*starlark.Builtin, starlark.Tuple, []starlark.Tuple) (map[string]any, error)) *starlark.Builtin {
		return starlark.NewBuiltin("", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			got, gotErr = read(b, args, kwargs)
			return starlark.None, nil
		})
	}
	predeclared := starlark.StringDict{
		"notify": record(func(b *starlark.Builtin, a starlark.Tuple, k []starlark.Tuple) (map[string]any, error) {
			return Send(b, a, k, "https://portal.example.com/portal/automations/s1/runs/r1")
		}),
		"publish": record(Publish),
	}
	if _, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", src, predeclared); err != nil {
		t.Fatalf("exec: %v", err)
	}
	return got, gotErr
}

func TestSend_TheFullCall(t *testing.T) {
	got, err := call(t, `notify(channel="ops", title="Feed stale", body="41m", key="feed:orders", repeat_after="6h",
		attachments=["a1", {"resource_id": "r1", "name": "raw.csv"}])`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"action": "send", "channel": "ops", "title": "Feed stale", "body": "41m",
		"link": "https://portal.example.com/portal/automations/s1/runs/r1",
		"key":  "feed:orders", "repeat_after": "6h",
		"attachments": []any{"a1", map[string]any{"resource_id": "r1", "name": "raw.csv"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %#v\nwant %#v", got, want)
	}
}

func TestSend_OmitsWhatTheScriptDidNotGive(t *testing.T) {
	got, err := call(t, `notify(channel="ops", title="t", link="https://example.com/d", attachments=["a1"])`)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"key", "repeat_after", "data"} {
		if _, ok := got[k]; ok {
			t.Errorf("payload carries %q the script did not give", k)
		}
	}
	if got["link"] != "https://example.com/d" {
		t.Errorf("link = %v, want the script's own", got["link"])
	}
	if !reflect.DeepEqual(got["attachments"], []any{"a1"}) {
		t.Errorf("attachments read as %#v", got["attachments"])
	}
}

func TestSend_Refusals(t *testing.T) {
	cases := map[string]string{
		`notify(channel="", title="t")`:                       "channel is empty",
		`notify(channel="ops", title="")`:                     "title is empty",
		`notify(channel="ops", title="t", attachments="a1")`:  "attachments must be a list",
		`notify(channel="ops", title="t", attachments=[len])`: "attachments must be strings and dicts",
		`notify(channel="ops", title="t", data=len)`:          "data must be JSON-shaped",
		`notify(channel="ops", title="t", repeat_after=3600)`: "repeat_after",
		`notify(channel="ops")`:                               "missing argument for title",
	}
	for src, want := range cases {
		if _, err := call(t, src); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", src, err, want)
		}
	}
}

func TestPublish(t *testing.T) {
	got, err := call(t, `publish(channel="reports", name="Weekly", message="Monday", key="weekly", repeat_after="24h")`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"action": "publish", "channel": "reports", "asset": "Weekly", "message": "Monday", "key": "weekly", "repeat_after": "24h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %#v, want %#v", got, want)
	}
	for src, msg := range map[string]string{
		`publish(channel="", name="x")`:   "channel is empty",
		`publish(channel="ops", name="")`: "name is empty",
		`publish(channel="ops")`:          "missing argument for name",
	} {
		if _, err := call(t, src); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: err = %v", src, err)
		}
	}
}

func TestPublish_AccessExpiryAndTarget(t *testing.T) {
	got, err := call(t, `publish(channel="slack", name="Weekly", access="public", expires_in="168h", target="C1")`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"action": "publish", "channel": "slack", "asset": "Weekly", "message": "",
		"access": "public", "expires_in": "168h", "target": "C1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
	got, err = call(t, `publish(channel="slack", name="Weekly")`)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"access", "expires_in", "target"} {
		if _, ok := got[key]; ok {
			t.Errorf("%s was sent although the script gave none, so the tool's default would not apply", key)
		}
	}
}

func TestSend_Target(t *testing.T) {
	got, err := call(t, `notify(channel="slack", title="t", target="C9")`)
	if err != nil {
		t.Fatal(err)
	}
	if got["target"] != "C9" {
		t.Errorf("target = %v", got["target"])
	}
}
