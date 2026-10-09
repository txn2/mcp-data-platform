package secretref

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// installSource installs a source for the test and puts the previous one
// back after it. Tests that install one do not run in parallel: the source
// is process-wide, as the platform's store is.
func installSource(t *testing.T, src ConnectionSource) {
	t.Helper()
	prev := SetConnectionSource(src)
	t.Cleanup(func() { SetConnectionSource(prev) })
}

// scoped answers "pat" only for connection "tableau".
func scoped(_ context.Context, name, connection string) (string, error) {
	if name == "pat" && connection == "tableau" {
		return "pat-value-1", nil
	}
	return "", errors.New("secret " + name + " may not be used by connection " + connection)
}

func TestFillConnection(t *testing.T) {
	installSource(t, scoped)
	ctx, r := WithRedactor(context.Background())

	got, err := FillConnection(ctx, "tableau", `{"token":"{{secret:pat}}"}`, JSONString)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"token":"pat-value-1"}` {
		t.Errorf("FillConnection = %s", got)
	}
	// The value is recorded, so a response echoing it is redacted.
	if out := r.String("echo pat-value-1"); out != "echo "+Redaction("pat") {
		t.Errorf("response not redacted: %s", out)
	}

	// The scope is the connection's: another connection is refused by name.
	if _, err := FillConnection(ctx, "other", "{{secret:pat}}", Raw); err == nil || !strings.Contains(err.Error(), "connection other") {
		t.Errorf("out-of-scope fill: %v", err)
	}
	// Text with no placeholder never reaches the source.
	if got, err := FillConnection(ctx, "other", "plain", Raw); err != nil || got != "plain" {
		t.Errorf("plain text: %q, %v", got, err)
	}
}

func TestFillConnectionWithoutSource(t *testing.T) {
	installSource(t, nil)
	_, err := FillConnection(context.Background(), "tableau", "{{secret:pat}}", Raw)
	if err == nil || !strings.Contains(err.Error(), `connection "tableau" names secret "pat"`) {
		t.Fatalf("missing source: %v", err)
	}
}

// A rotated value is read on the next fill: nothing is cached across fills.
func TestFillConnectionReadsEachTime(t *testing.T) {
	value := "first-value"
	installSource(t, func(context.Context, string, string) (string, error) { return value, nil })
	a, _ := FillConnection(context.Background(), "c", "{{secret:x}}", Raw)
	value = "second-value"
	b, _ := FillConnection(context.Background(), "c", "{{secret:x}}", Raw)
	if a != "first-value" || b != "second-value" {
		t.Errorf("fills = %q, %q", a, b)
	}
}

func TestSetConnectionSourceReturnsPrevious(t *testing.T) {
	installSource(t, scoped)
	prev := SetConnectionSource(nil)
	if prev == nil {
		t.Fatal("the installed source was not returned")
	}
	if SetConnectionSource(prev) != nil {
		t.Error("removing a source left one installed")
	}
}

func TestPlaceholderHelpers(t *testing.T) {
	if got := Names("{{secret:a}} and {{secret:b.c}} and {{secret:a}}"); !slices.Equal(got, []string{"a", "b.c"}) {
		t.Errorf("Names = %v", got)
	}
	if Names("plain") != nil {
		t.Error("Names of plain text")
	}
	for s, want := range map[string]bool{"{{secret:ok}}": false, "{{secret:Bad}}": true, "x {{secret:": true, "plain": false} {
		if Malformed(s) != want {
			t.Errorf("Malformed(%q) = %v", s, !want)
		}
	}
	if got := WithoutPlaceholders("svc:{{secret:user}}"); got != "svc:" {
		t.Errorf("WithoutPlaceholders = %q", got)
	}
	if got := WithoutPlaceholders("plain"); got != "plain" {
		t.Errorf("WithoutPlaceholders(plain) = %q", got)
	}
	for s, want := range map[string]bool{"{{secret:pat}}": true, "Bearer {{secret:pat}}": false, "{{secret:pat}} ": false, "": false} {
		if IsPlaceholder(s) != want {
			t.Errorf("IsPlaceholder(%q) = %v", s, !want)
		}
	}
}

// A {{totp:<name>}} placeholder is looked up under TOTPName, wherever a
// request carries one, and its code is not recorded for redaction (#2065).
func TestOneTimeCodePlaceholders(t *testing.T) {
	codes := func(name string) (string, error) {
		if secret, ok := IsTOTPName(name); ok && secret == "mfa" {
			return "287082", nil
		}
		return "pw-value", nil
	}
	ctx, r := WithRedactor(context.Background())
	lookup := r.Recording(codes)

	got, err := Fill(`{"code":"{{totp:mfa}}","pw":"{{secret:pw}}"}`, lookup, JSONString)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"code":"287082","pw":"pw-value"}` {
		t.Errorf("Fill = %s", got)
	}
	if out := r.String("287082 pw-value"); out != "287082 "+Redaction("pw") {
		t.Errorf("the code was redacted or the value was not: %s", out)
	}
	if got, err := FillPath("/v/%7B%7Btotp:mfa%7D%7D/{{totp:mfa}}", codes); err != nil || got != "/v/287082/287082" {
		t.Errorf("FillPath = %q, %v", got, err)
	}
	if !HasPlaceholder("{{totp:mfa}}") || Names("{{totp:mfa}}")[0] != TOTPName("mfa") {
		t.Error("a code placeholder was not recognized")
	}
	if !Malformed("{{totp:MFA}}") {
		t.Error("a malformed code placeholder passed")
	}
	if _, err := Fill("{{totp:", codes, Raw); err == nil {
		t.Error("an opened code placeholder was sent")
	}

	// A connection's own configuration never fills a code.
	installSource(t, scoped)
	if _, err := FillConnection(ctx, "tableau", "{{totp:mfa}}", Raw); err == nil || !strings.Contains(err.Error(), "filled only in an api call's request") {
		t.Errorf("FillConnection(totp) = %v", err)
	}
}
