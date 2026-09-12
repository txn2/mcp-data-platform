//go:build integration

package acceptance

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// Issue #1690: manage_resource action=get answers the same shape for an absent
// file however the file was named. A reference that names nothing you can see
// is found=false beside the reference it looked up, exactly as an empty address
// is found=false beside the address, and exactly as fetch answers the same
// dangling reference. A store that could not answer, and a reference that is
// not a managed-resource reference at all, both stay tool errors.
//
// Every criterion runs through the real MCP surface against the running stack:
// the platform's own managed-resource library and the real fetch tool.
//
// Wire forms: the parameters this ticket touches are typed in manage_resource's
// schema, which is closed to unknown keys, so each admits exactly ONE JSON
// form. `action`, `reference`, `path` and `filename` are strings. fetch's
// `reference` is a string too. The one form of each is what every check below
// sends as literal tools/call params.
const (
	issue1690Folder  = "acceptance/issue-1690"
	issue1690Purpose = "Acceptance for #1690: reading a managed resource that a reference names, to see how absence is reported."
)

// issue1690DanglingRef is a well-formed managed-resource reference in the id
// shape the platform issues, naming a resource that does not exist. It is
// generated per run so a file created by an earlier run cannot answer it.
func issue1690DanglingRef(t *testing.T) string {
	t.Helper()
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("generating a reference id: %v", err)
	}
	return "mcp:resource:" + hex.EncodeToString(raw[:])
}

// TestIssue1690_AReferenceThatNamesNothingIsFoundFalse is the ticket's first
// criterion: get by a dangling reference is an answer, not a tool error, and it
// names the reference it looked up.
func TestIssue1690_AReferenceThatNamesNothingIsFoundFalse(t *testing.T) {
	c := connect(t)
	ref := issue1690DanglingRef(t)

	res, text, err := c.callRaw("manage_resource", map[string]any{
		"action": "get", "reference": ref,
	})
	if err != nil {
		t.Fatalf("manage_resource get: transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("get by a reference that names nothing is still a tool error: %s", text)
	}

	out := c.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if found, _ := out["found"].(bool); found {
		t.Fatalf("found=true for a reference that names nothing: %v", out)
	}
	if got, _ := out["reference"].(string); got != ref {
		t.Errorf("reference = %q; want the reference that was looked up, %q", got, ref)
	}
	if _, ok := out["resource"]; ok {
		t.Errorf("an absent file carries a resource record: %v", out)
	}
	if msg, _ := out["message"].(string); !strings.Contains(msg, ref) {
		t.Errorf("the message does not name the reference: %q", msg)
	}
}

// TestIssue1690_TheThreeSurfacesAgreeOnAbsence is the ticket's central
// criterion: get by reference, get by address, and fetch answer the same
// dangling question the same way.
func TestIssue1690_TheThreeSurfacesAgreeOnAbsence(t *testing.T) {
	c := connect(t)
	ref := issue1690DanglingRef(t)

	byRef := c.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if found, _ := byRef["found"].(bool); found {
		t.Fatalf("get by reference: found=true for a reference that names nothing: %v", byRef)
	}

	byAddress := c.call("manage_resource", map[string]any{
		"action": "get", "path": issue1690Folder,
		"filename": "never-written.csv",
	})
	if found, _ := byAddress["found"].(bool); found {
		t.Fatalf("get by address: found=true at an empty address: %v", byAddress)
	}

	fetched := c.call("fetch", map[string]any{"reference": ref, "purpose": issue1690Purpose})
	if found, _ := fetched["found"].(bool); found {
		t.Fatalf("fetch: found=true for a reference that names nothing: %v", fetched)
	}
}

// TestIssue1690_ALiveReferenceStillReadsAsFound holds the answer for a file
// that is there: the reference branch keeps reporting the record, and now
// reports the reference beside it.
func TestIssue1690_ALiveReferenceStillReadsAsFound(t *testing.T) {
	c := connect(t)
	filename := "live-" + hex.EncodeToString([]byte(time.Now().Format("150405.000000"))) + ".csv"

	created := c.call("manage_resource", map[string]any{
		"action": "create", "path": issue1690Folder, "filename": filename,
		"display_name": "Acceptance 1690 " + filename,
		"description":  "Written by the #1690 acceptance run.",
		"content":      "day,high\nmon,71\n", "content_type": "text/csv",
		"if_exists": "replace",
	})
	ref, _ := created["reference"].(string)
	if !strings.HasPrefix(ref, "mcp:resource:") {
		t.Fatalf("the create does not name the file: %v", created)
	}

	got := c.call("manage_resource", map[string]any{"action": "get", "reference": ref})
	if found, _ := got["found"].(bool); !found {
		t.Fatalf("found=false for a live reference: %v", got)
	}
	if reported, _ := got["reference"].(string); reported != ref {
		t.Errorf("reference = %q; want %q", reported, ref)
	}
	if _, ok := got["resource"].(map[string]any); !ok {
		t.Errorf("a live reference carries no resource record: %v", got)
	}
}

// TestIssue1690_AReferenceThatIsNotOneStaysAnError holds the other half of the
// contract: absence became an answer, and a caller mistake did not. A string
// that is not a reference the platform issues, and a reference naming a target
// of another type, are both refused rather than reported as an absent file.
func TestIssue1690_AReferenceThatIsNotOneStaysAnError(t *testing.T) {
	c := connect(t)

	for _, tc := range []struct {
		name      string
		reference string
	}{
		{"not a platform reference", "not-a-reference"},
		{"an asset, which has no managed-resource content", "mcp:asset:00000000-0000-0000-0000-000000000000"},
	} {
		res, text, err := c.callRaw("manage_resource", map[string]any{
			"action": "get", "reference": tc.reference,
		})
		if err != nil {
			t.Fatalf("%s: transport error: %v", tc.name, err)
		}
		if !res.IsError {
			t.Errorf("%s: %q was answered rather than refused: %s", tc.name, tc.reference, text)
		}
	}
}
