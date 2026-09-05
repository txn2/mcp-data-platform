//go:build integration

package acceptance

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Issue #1583: a registration made with repair corrected a later defective
// version of its file under the right registrant, but the sentence reporting
// the correction was attached to whichever registration the follow loop
// reached first -- the newest one over the file, whether or not it carried the
// choice. Someone who registered with repair off was told their table rewrote
// somebody's file, and where the two registrations belong to different people
// the wrong person was named.
//
// What these hold: the correction sentence appears on the outcome of the
// registration carrying repair, whatever order the two registrations were made
// in; the one without it reports only that it moved; the correction is still
// reported exactly once per write; and a write that corrects nothing reports
// no correction sentence at all.
//
// Wire forms: `repair` is a boolean, so a register call admits it as true, as
// false and as absent -- all three are sent below. The write that produces the
// defective version admits its bytes as `content` (a string) and as
// `content_base64`, and one order is written with each.

// torn1583 is the version a scheduled export produces: a line break inside a
// quoted cell, which a query engine reads as the end of the row.
const torn1583 = "store_id,address,units\n" +
	"101,\"12 Mill Rd\nSuite 4\",10\n" +
	"102,\"9 Bay St\nSeattle WA\",20\n"

// clean1583 is a version of the same file with nothing to correct.
const clean1583 = "store_id,address,units\n101,12 Mill Rd,10\n102,9 Bay St,20\n"

// register1583 registers a following table over a file and drops it when the
// test ends. repair is passed as the caller gives it: a *bool, so the absent
// form is reachable alongside true and false.
func register1583(t *testing.T, c *client, reference, table string, repair *bool) string {
	t.Helper()
	args := map[string]any{
		"action": "register", "reference": reference, "connection": scratchResourceConnection,
		"table_name": table, "follow": true,
	}
	if repair != nil {
		args["repair"] = *repair
	}
	registered := c.call("manage_table", args)
	id, _ := registered["registration_id"].(string)
	queryTable, _ := registered["query_table"].(string)
	if id == "" || queryTable == "" {
		t.Fatalf("manage_table register returned no registration: %v", registered)
	}
	if got, _ := registered["repair"].(bool); got != (repair != nil && *repair) {
		t.Fatalf("the registration does not carry the choice it was made with: %v", registered)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("manage_table", map[string]any{"action": "unregister", "registration_id": id})
	})
	return queryTable
}

// replace1583 writes a new version of the file and returns the sentences the
// write reports about the tables over it, one per line as the caller reads
// them.
func replace1583(t *testing.T, c *client, reference string, content map[string]any) []string {
	t.Helper()
	call := map[string]any{
		"action": "replace_content", "reference": reference,
		"change_summary": "Acceptance #1583: the next version from the same source.",
	}
	for k, v := range content {
		call[k] = v
	}
	replaced := c.call("manage_resource", call)
	lines, _ := replaced["tables"].([]any)
	if len(lines) == 0 {
		t.Fatalf("the write said nothing about the tables over the file: %v", replaced)
	}
	said := make([]string, 0, len(lines))
	for _, line := range lines {
		text, _ := line.(string)
		said = append(said, text)
	}
	return said
}

// sentenceFor is what the write said about one table.
func sentenceFor(t *testing.T, said []string, queryTable string) string {
	t.Helper()
	for _, line := range said {
		if strings.HasPrefix(line, queryTable+" on ") {
			return line
		}
	}
	t.Fatalf("the write said nothing about %s: %v", queryTable, said)
	return ""
}

// TestIssue1583_TheCorrectionIsReportedOnTheRegistrationThatAskedForIt covers
// criteria 1, 2, 3, 4 and 5: two registrations sit over one file, one carrying
// the repair choice and one not, made in each of the two possible orders. The
// correction sentence belongs to the registration that asked for it in both.
func TestIssue1583_TheCorrectionIsReportedOnTheRegistrationThatAskedForIt(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name string
		// repairingFirst decides which of the two registrations was made
		// first, which is what the store's newest-first order inverts.
		repairingFirst bool
		// withoutChoice is how the registration that does not correct the
		// file says so: explicitly off, or by leaving the field out.
		withoutChoice *bool
		// content is the wire form the defective version is written in.
		content map[string]any
	}{
		{
			name:           "the repairing registration was made first",
			repairingFirst: true,
			withoutChoice:  &off,
			content:        map[string]any{"content": torn1583},
		},
		{
			name:           "the repairing registration was made last",
			repairingFirst: false,
			withoutChoice:  nil,
			content: map[string]any{
				"content_base64": base64.StdEncoding.EncodeToString([]byte(torn1583)),
				"content_type":   "text/csv",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := connectAs(t, devOwnerAPIKey)
			admin := connect(t)
			stamp := fmt.Sprintf("%d", time.Now().UnixNano())

			created := owner.call("manage_resource", map[string]any{
				"action":       "create",
				"display_name": "Acceptance 1583 " + stamp,
				"description":  "Acceptance: two registrations over one file, one of which corrects it.",
				"filename":     "acc-1583-" + stamp + ".csv",
				"path":         "acceptance/issue-1583",
				"content_type": "text/csv",
				"content":      clean1583,
			})
			reference, _ := created["reference"].(string)
			if reference == "" {
				t.Fatalf("manage_resource create returned no reference: %v", created)
			}

			// The two registrations, made in the order this case is about. The
			// one that corrects the file is the owner's; the one that does not
			// is the administrator's, so a sentence on the wrong outcome names
			// the wrong person as well as the wrong table.
			on := true
			var corrects, plain string
			registerRepairing := func() {
				corrects = register1583(t, owner, reference, "acc1583r_"+stamp, &on)
			}
			registerPlain := func() {
				plain = register1583(t, admin, reference, "acc1583p_"+stamp, tc.withoutChoice)
			}
			if tc.repairingFirst {
				registerRepairing()
				registerPlain()
			} else {
				registerPlain()
				registerRepairing()
			}

			// Criterion 4: a version with nothing to correct is reported as a
			// move and nothing else, on either registration.
			said := replace1583(t, admin, reference, map[string]any{"content": clean1583})
			for _, line := range said {
				if strings.Contains(line, "Saved version") {
					t.Fatalf("a write that corrected nothing reported a correction: %v", said)
				}
			}

			// The defective version, written by the administrator rather than
			// by either registrant.
			said = replace1583(t, admin, reference, tc.content)

			// Criterion 1: the correction is reported on the registration that
			// asked for the file to be corrected.
			asked := sentenceFor(t, said, corrects)
			if !strings.Contains(asked, "now reads version 4.") {
				t.Fatalf("the correcting table did not move onto the corrected version: %q", asked)
			}
			if !strings.Contains(asked, "Saved version 4 of this file, which put 2 rows back onto one line.") {
				t.Fatalf("the registration that asked for the correction is not credited with it: %q", asked)
			}

			// Criterion 2: the other one reports only that it moved.
			other := sentenceFor(t, said, plain)
			if other != plain+" on "+scratchResourceConnection+" now reads version 4." {
				t.Fatalf("the registration made without the choice was told it rewrote the file: %q", other)
			}

			// Criterion 3: one correction, one sentence about it.
			var reported int
			for _, line := range said {
				if strings.Contains(line, "Saved version 4 of this file") {
					reported++
				}
			}
			if reported != 1 {
				t.Fatalf("the correction is reported %d times, not once: %v", reported, said)
			}
		})
	}
}
