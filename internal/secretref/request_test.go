package secretref

import (
	"testing"
)

// TestFillRequest fills each part a placeholder may sit in, escaping a
// string body for what it is, and records every value it filled.
func TestFillRequest(t *testing.T) {
	r := &Redactor{}
	in := Request{
		Path:        "/u/{{secret:token}}",
		Headers:     map[string]string{"X-K": "{{secret:token}}"},
		Query:       map[string]any{"q": "{{secret:token}}"},
		Body:        "user=a&pw={{secret:portal_password}}",
		ContentType: "application/x-www-form-urlencoded",
	}
	out, err := FillRequest(in, r.Recording(lookup))
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != "/u/tok-123456" || out.Headers["X-K"] != "tok-123456" || out.Query["q"] != "tok-123456" || out.Body != "user=a&pw=pa%22ss+w%2Frd" {
		t.Errorf("FillRequest = %+v", out)
	}
	if in.Path != "/u/{{secret:token}}" {
		t.Error("the caller's request was changed")
	}
	if r.String("tok-123456") != "[REDACTED:token]" {
		t.Error("a filled value was not recorded")
	}
	jsonText, _ := FillRequest(Request{Body: `{"p":"{{secret:portal_password}}"}`}, lookup)
	plain, _ := FillRequest(Request{Body: "pw {{secret:portal_password}}"}, lookup)
	object, _ := FillRequest(Request{Body: map[string]any{"p": "{{secret:token}}"}}, lookup)
	filled, _ := object.Body.(map[string]any)
	if jsonText.Body != `{"p":"pa\"ss w/rd"}` || plain.Body != `pw pa"ss w/rd` || filled["p"] != "tok-123456" {
		t.Errorf("bodies filled as %v / %v / %v", jsonText.Body, plain.Body, object.Body)
	}
	for _, bad := range []Request{
		{Path: "/{{secret:nope}}"},
		{Headers: map[string]string{"k": "{{secret:nope}}"}},
		{Query: map[string]any{"k": "{{secret:nope}}"}},
		{Body: "{{secret:nope}}"},
	} {
		if _, err := FillRequest(bad, r.Recording(lookup)); err == nil {
			t.Errorf("%+v filled an unknown name", bad)
		}
	}
}
