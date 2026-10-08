package secretref

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// lookup knows two secrets and refuses every other name by name.
func lookup(name string) (string, error) {
	switch name {
	case "portal_password":
		return `pa"ss w/rd`, nil
	case "token":
		return "tok-123456", nil
	}
	return "", errors.New("no secret named " + name)
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"portal_password": true, "a.b-c": true, "9x": true, "": false, "Upper": false, "-lead": false, "has space": false, strings.Repeat("a", 64): false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

func TestFill(t *testing.T) {
	got, err := Fill(`{"text": "{{secret:portal_password}}", "t": "Bearer {{secret:token}}"}`, lookup, JSONString)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"text": "pa\"ss w/rd", "t": "Bearer tok-123456"}`; got != want {
		t.Errorf("Fill = %s, want %s", got, want)
	}
	if got, _ := Fill("plain", lookup, Raw); got != "plain" {
		t.Error("text without a placeholder changed")
	}
	if _, err := Fill("{{secret:nope}}", lookup, Raw); err == nil || err.Error() != "no secret named nope" {
		t.Errorf("unknown name: %v", err)
	}
	for _, bad := range []string{"{{secret:Bad}}", "{{secret:x", "{{secret:}}", "a {{secret:token}} {{secret:"} {
		if _, err := Fill(bad, lookup, Raw); !errors.Is(err, ErrMalformed) {
			t.Errorf("Fill(%q) err = %v, want malformed", bad, err)
		}
	}
}

func TestFillPath(t *testing.T) {
	got, err := FillPath("/users/%7B%7Bsecret:token%7D%7D/x/{{secret:portal_password}}", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/users/tok-123456/x/pa%22ss%20w%2Frd"; got != want {
		t.Errorf("FillPath = %s, want %s", got, want)
	}
	if _, err := FillPath("/a/%7B%7Bsecret:nope%7D%7D", lookup); err == nil {
		t.Error("unknown escaped name was filled")
	}
}

func TestFillValueCopies(t *testing.T) {
	in := map[string]any{"user": "u", "{{secret:token}}": []any{"{{secret:portal_password}}", 3, map[string]any{"k": "{{secret:token}}"}}}
	out, err := FillValue(in, lookup)
	if err != nil {
		t.Fatal(err)
	}
	filled, _ := out.(map[string]any)
	list, _ := filled["tok-123456"].([]any)
	nested, _ := list[2].(map[string]any)
	if list[0] != `pa"ss w/rd` || list[1] != 3 || nested["k"] != "tok-123456" {
		t.Errorf("FillValue = %v", out)
	}
	if kept, _ := in["{{secret:token}}"].([]any); kept[0] != "{{secret:portal_password}}" {
		t.Error("FillValue changed the caller's value")
	}
	for _, bad := range []any{map[string]any{"{{secret:nope}}": 1}, map[string]any{"k": "{{secret:nope}}"}, []any{"{{secret:nope}}"}} {
		if _, err := FillValue(bad, lookup); err == nil {
			t.Errorf("FillValue(%v) filled an unknown name", bad)
		}
	}
	m, err := FillStrings(map[string]string{"X-Key": "{{secret:token}}"}, lookup)
	if err != nil || m["X-Key"] != "tok-123456" {
		t.Errorf("FillStrings = %v, %v", m, err)
	}
	if m, err := FillStrings(nil, lookup); err != nil || len(m) != 0 {
		t.Error("FillStrings(nil)")
	}
	if _, err := FillStrings(map[string]string{"k": "{{secret:nope}}"}, lookup); err == nil {
		t.Error("FillStrings filled an unknown name")
	}
}

func TestRedactor(t *testing.T) {
	var nilR *Redactor
	nilR.Add("x", "value!")
	if !nilR.Empty() || nilR.String("a") != "a" {
		t.Error("nil redactor")
	}
	r := &Redactor{}
	r.Add("pw", `pa"ss w/rd`)
	r.Add("pw", `pa"ss w/rd`)
	r.Add("empty", "")
	cases := map[string]string{
		`value=pa"ss w/rd;`:          "value=[REDACTED:pw];",
		`{"v":"pa\"ss w/rd"}`:        `{"v":"[REDACTED:pw]"}`,
		`q=pa%22ss+w%2Frd`:           "q=[REDACTED:pw]",
		`/p/pa%22ss%20w%2Frd`:        "/p/[REDACTED:pw]",
		`twice pa"ss w/rdpa"ss w/rd`: "twice [REDACTED:pw][REDACTED:pw]",
		`nothing to see`:             "nothing to see",
	}
	for in, want := range cases {
		if got := r.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
	h := map[string][]string{"X-Echo": {`pa"ss w/rd`}}
	r.Strings(h)
	if h["X-Echo"][0] != "[REDACTED:pw]" {
		t.Errorf("Strings = %v", h)
	}
	nilR.Strings(h)
	if r.Error(errors.New(`bad pa"ss w/rd`)) != "bad [REDACTED:pw]" || r.Error(nil) != "" {
		t.Error("Error")
	}
}

// TestReaderAcrossChunks feeds the stream one byte at a time, so every value
// straddles a read boundary.
func TestReaderAcrossChunks(t *testing.T) {
	r := &Redactor{}
	r.Add("token", "tok-123456")
	r.Add("pw", "secret-pw")
	body := strings.Repeat("x", 100) + "tok-123456 tok-12345 secret-pw" + strings.Repeat("y", 50000) + "tok-123"
	got, err := io.ReadAll(r.Reader(io.NopCloser(iotest.OneByteReader(strings.NewReader(body)))))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("x", 100) + "[REDACTED:token] tok-12345 [REDACTED:pw]" + strings.Repeat("y", 50000) + "tok-123"
	if string(got) != want {
		t.Errorf("stream redaction wrong; got %d bytes, want %d", len(got), len(want))
	}
	if err := r.Reader(io.NopCloser(strings.NewReader(""))).Close(); err != nil {
		t.Error(err)
	}
	empty := &Redactor{}
	src := io.NopCloser(strings.NewReader("a"))
	if empty.Reader(src) != src {
		t.Error("an empty redactor wrapped the stream")
	}
	_, err = io.ReadAll(r.Reader(io.NopCloser(iotest.ErrReader(errors.New("boom")))))
	if err == nil || err.Error() != "boom" {
		t.Errorf("stream error = %v", err)
	}
}

func TestContext(t *testing.T) {
	if FromContext(context.Background()) != nil {
		t.Error("a bare context carries a redactor")
	}
	ctx, r := WithRedactor(context.Background())
	if FromContext(ctx) != r {
		t.Error("FromContext")
	}
	if Redaction("x") != "[REDACTED:x]" {
		t.Error("Redaction")
	}
}
