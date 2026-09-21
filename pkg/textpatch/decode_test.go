package textpatch_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// decodeEdits decodes an edits array exactly as a tool's input struct does, so
// these tests exercise the path a tools/call argument travels.
func decodeEdits(t *testing.T, body string) []textpatch.Edit {
	t.Helper()
	var edits []textpatch.Edit
	if err := json.Unmarshal([]byte(body), &edits); err != nil {
		t.Fatalf("decoding edits: %v", err)
	}
	return edits
}

// applyJSON applies edits decoded from JSON and returns the body and the
// patch error, which is the value under test rather than something to wrap.
func applyJSON(t *testing.T, doc, editsJSON string) (body string, patchErr error) {
	t.Helper()
	res, err := textpatch.Apply(doc, decodeEdits(t, editsJSON), textpatch.Options{})
	return res.Body, err //nolint:wrapcheck // the error is the assertion
}

const threeLines = "AAA = \"one\"\nBBB = \"two\"\nCCC = \"three\"\n"

// TestReplaceWithoutReplaceKeyIsRefused is issue #1804: an edit whose
// replacement rode on an unrecognized key had the key ignored, the anchor
// replaced with an empty string, and a new version saved reporting success.
func TestReplaceWithoutReplaceKeyIsRefused(t *testing.T) {
	body, err := applyJSON(t, threeLines,
		`[{"op":"replace","find":"BBB = \"two\"","text":"BBB = \"CHANGED\""}]`)
	if err == nil {
		t.Fatalf("expected a refusal, got body %q", body)
	}
	var pErr *textpatch.Error
	if !errors.As(err, &pErr) {
		t.Fatalf("expected a *textpatch.Error, got %T: %v", err, err)
	}
	if pErr.Code != textpatch.CodeBadEdit {
		t.Errorf("code = %q, want %q", pErr.Code, textpatch.CodeBadEdit)
	}
	if pErr.EditIndex != 0 {
		t.Errorf("edit index = %d, want 0", pErr.EditIndex)
	}
	// The message must name the key that was not sent and the refusal must
	// teach the deliberate form, because the reason the bug was expensive is
	// that nothing said which key carried the replacement.
	if !strings.Contains(pErr.Message, `"replace"`) {
		t.Errorf("message does not name the missing key: %q", pErr.Message)
	}
	if !strings.Contains(pErr.Hint, `"replace": ""`) {
		t.Errorf("hint does not show the explicit deletion form: %q", pErr.Hint)
	}
}

// TestUnknownEditKeyIsRefused covers the other half of #1804: the offending key
// is named rather than ignored.
func TestUnknownEditKeyIsRefused(t *testing.T) {
	_, err := applyJSON(t, threeLines,
		`[{"op":"replace","find":"BBB = \"two\"","replace":"x","replacement":"y","with":"z"}]`)
	if err == nil {
		t.Fatal("expected a refusal for unknown edit keys")
	}
	for _, want := range []string{`"replacement"`, `"with"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
	var pErr *textpatch.Error
	if !errors.As(err, &pErr) {
		t.Fatalf("expected a *textpatch.Error, got %T", err)
	}
	// The accepted set is what turns the refusal into instruction.
	for _, want := range []string{`"replace"`, `"find"`, `"op"`, `"text"`} {
		if !strings.Contains(pErr.Hint, want) {
			t.Errorf("hint does not list %s as accepted: %q", want, pErr.Hint)
		}
	}
}

// TestExplicitEmptyReplaceDeletes keeps deletion available: it is the omission
// that is refused, never the written-out empty string.
func TestExplicitEmptyReplaceDeletes(t *testing.T) {
	body, err := applyJSON(t, threeLines,
		`[{"op":"replace","find":"BBB = \"two\"\n","replace":""}]`)
	if err != nil {
		t.Fatalf("explicit empty replacement refused: %v", err)
	}
	if want := "AAA = \"one\"\nCCC = \"three\"\n"; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// TestReplaceKeyStillReplaces is the control: the correct key behaves exactly
// as before, including a multi-line replacement that adds lines.
func TestReplaceKeyStillReplaces(t *testing.T) {
	body, err := applyJSON(t, threeLines,
		`[{"find":"BBB = \"two\"","replace":"BBB = \"CHANGED\"\nBBB2 = \"added\""}]`)
	if err != nil {
		t.Fatalf("replace refused: %v", err)
	}
	want := "AAA = \"one\"\nBBB = \"CHANGED\"\nBBB2 = \"added\"\nCCC = \"three\"\n"
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// TestTextOpsRequireTheTextKey covers the same omission on every operation that
// writes from "text": an insert or a section rewrite with no payload key is the
// identical silent-emptying bug.
func TestTextOpsRequireTheTextKey(t *testing.T) {
	cases := []struct {
		name  string
		edits string
	}{
		{"insert_before", `[{"op":"insert_before","find":"BBB = \"two\"","replace":"x"}]`},
		{"insert_after", `[{"op":"insert_after","find":"BBB = \"two\"","replace":"x"}]`},
		{"append", `[{"op":"append"}]`},
		{"prepend", `[{"op":"prepend"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := applyJSON(t, threeLines, tc.edits)
			if err == nil {
				t.Fatalf("expected a refusal, got body %q", body)
			}
			if !strings.Contains(err.Error(), `"text"`) {
				t.Errorf("refusal does not name the missing key: %v", err)
			}
		})
	}
}

// TestReplaceSectionRequiresText is the markdown counterpart, where the
// omission deletes a whole section rather than one anchor.
func TestReplaceSectionRequiresText(t *testing.T) {
	doc := "# Title\n\n## Methodology\n\nbody text\n\n## Results\n\nmore\n"
	body, err := applyJSON(t, doc, `[{"op":"replace_section","section":"## Methodology"}]`)
	if err == nil {
		t.Fatalf("expected a refusal, got body %q", body)
	}
	if !strings.Contains(err.Error(), `"text"`) {
		t.Errorf("refusal does not name the missing key: %v", err)
	}
}

// TestEditComposedInGoIsTakenAtItsWord pins the boundary the presence tracking
// draws: the refusal is about what a JSON caller sent, so a platform component
// that builds an edit in Go — the managed-script data-region splice, the
// agent-instruction sink — is unaffected by it.
func TestEditComposedInGoIsTakenAtItsWord(t *testing.T) {
	res, err := textpatch.Apply(threeLines, []textpatch.Edit{{
		Op:      textpatch.OpReplace,
		Find:    "BBB = \"two\"\n",
		Replace: "",
	}}, textpatch.Options{})
	if err != nil {
		t.Fatalf("Go-composed deletion refused: %v", err)
	}
	if want := "AAA = \"one\"\nCCC = \"three\"\n"; res.Body != want {
		t.Errorf("body = %q, want %q", res.Body, want)
	}
}

// TestNothingIsWrittenWhenOneEditIsMalformed proves the refusal keeps the
// all-or-nothing promise: a good edit ahead of a malformed one does not land.
func TestNothingIsWrittenWhenOneEditIsMalformed(t *testing.T) {
	body, err := applyJSON(t, threeLines, `[
		{"op":"replace","find":"AAA = \"one\"","replace":"AAA = \"first\""},
		{"op":"replace","find":"CCC = \"three\"","text":"CCC = \"third\""}
	]`)
	if err == nil {
		t.Fatalf("expected a refusal, got body %q", body)
	}
	if body != "" {
		t.Errorf("a refused call returned a body: %q", body)
	}
	var pErr *textpatch.Error
	if !errors.As(err, &pErr) {
		t.Fatalf("expected a *textpatch.Error, got %T", err)
	}
	if pErr.EditIndex != 1 {
		t.Errorf("edit index = %d, want 1 (the malformed edit)", pErr.EditIndex)
	}
}
