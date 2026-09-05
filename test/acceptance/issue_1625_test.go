//go:build integration

package acceptance

import (
	"fmt"
	"testing"
	"time"
)

// Issue #1625: the memory staleness watcher marked records verified that it
// never checked, and re-dated every record's updated_at each pass. On the
// deployment measured, all 52 records carried an updated_at minutes old while
// the newest content edit on most of them was weeks old, and 34 of them named
// no entity at all, so `last_verified` on those meant only that the watcher's
// cursor had passed the row. After this change, updated_at moves on a content
// write and on nothing else, and last_verified is set only on records the
// watcher can actually check.
//
// Wire forms: every parameter this file touches is typed and admits one JSON
// form -- memory_capture's type, content, category, confidence and entity_urns
// (an array of strings), and memory_manage's command, id, content, limit and
// offset. Neither tool takes an untyped parameter, so there is one form to
// send, and neither takes `purpose`: memory is not a data-access tool, and its
// schema refuses the argument rather than ignoring it.

// memoryListPages bounds how far the suite pages the caller's own memory
// listing looking for the record it just wrote.
const memoryListPages = 5

// captureMemory1625 writes one memory record through the tool a person uses
// and returns its id.
func captureMemory1625(t *testing.T, c *client, content, sinkClass string, entityURNs []any) string {
	t.Helper()
	args := map[string]any{
		"type":       sinkClass,
		"content":    content,
		"category":   "business_context",
		"confidence": "high",
	}
	if entityURNs != nil {
		args["entity_urns"] = entityURNs
	}
	out := c.call("memory_capture", args)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("memory_capture returned no id: %v", out)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("memory_manage", map[string]any{"command": "forget", "id": id})
	})
	return id
}

// memoryRecord1625 reads one record back through memory_manage list, which is
// the listing the portal Memory view and review_duplicates read, and the one
// updated_at is a sortable column of.
func memoryRecord1625(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	for page := range memoryListPages {
		out := c.call("memory_manage", map[string]any{
			"command": "list",
			"limit":   100,
			"offset":  page * 100,
		})
		records, _ := out["records"].([]any)
		for _, entry := range records {
			record, _ := entry.(map[string]any)
			if got, _ := record["id"].(string); got == id {
				return record
			}
		}
		if len(records) < 100 {
			break
		}
	}
	t.Fatalf("memory record %s is not in the caller's own listing", id)
	return nil
}

// updatedAt1625 reads a record's updated_at, the column this ticket is about:
// what a reader takes as when the memory last changed.
func updatedAt1625(t *testing.T, record map[string]any) time.Time {
	t.Helper()
	raw, _ := record["updated_at"].(string)
	if raw == "" {
		t.Fatalf("record carries no updated_at: %v", record)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("record updated_at = %q is not a timestamp: %v", raw, err)
	}
	return parsed
}

// TestIssue1625_UpdatedAtMovesOnAContentWrite is criterion 2, and the control
// for the criteria below: the column still means what a reader takes it to
// mean, so an assertion that it did not move is about the watcher and not
// about a column nothing writes.
func TestIssue1625_UpdatedAtMovesOnAContentWrite(t *testing.T) {
	c := connect(t)
	id := captureMemory1625(t, c,
		fmt.Sprintf("Acceptance #1625: the refund window is 30 days (%d).", time.Now().UnixNano()),
		"business_knowledge", nil)

	before := updatedAt1625(t, memoryRecord1625(t, c, id))
	time.Sleep(time.Second)

	c.call("memory_manage", map[string]any{
		"command": "update",
		"id":      id,
		"content": fmt.Sprintf("Acceptance #1625: the refund window is 45 days (%d).", time.Now().UnixNano()),
	})

	after := updatedAt1625(t, memoryRecord1625(t, c, id))
	if !after.After(before) {
		t.Errorf("updated_at = %s after a content write, want later than %s", after, before)
	}
}

// TestIssue1625_AnEntitylessRecordIsNotVerified is criterion 1 as a person sees
// it: a record naming no entity is not something the watcher can check, so it
// carries no last_verified. The column staying null is what review_stale and
// the portal must read as "not subject to catalog verification" rather than as
// a fresh check nothing performed.
func TestIssue1625_AnEntitylessRecordIsNotVerified(t *testing.T) {
	c := connect(t)
	id := captureMemory1625(t, c,
		fmt.Sprintf("Acceptance #1625: revenue is reported net of refunds (%d).", time.Now().UnixNano()),
		"business_knowledge", nil)

	record := memoryRecord1625(t, c, id)
	if v := record["last_verified"]; v != nil && v != "" {
		t.Errorf("last_verified = %v on a record naming no entity; the watcher cannot check one, so it must not stamp it", v)
	}
}

// TestIssue1625_TheWatcherVerifiesOnlyWhatItCanCheck is criterion 1 through
// the real surface: the watcher runs on this stack on a seconds-long interval
// (dev/platform.yaml), so this waits for an actual tick rather than assuming
// one. The entity-linked record is stamped verified; the record naming no
// entity is not; and neither record's updated_at moves, because verification
// reads the catalog and changes nothing about the record.
func TestIssue1625_TheWatcherVerifiesOnlyWhatItCanCheck(t *testing.T) {
	c := connect(t)
	linked := captureMemory1625(t, c,
		fmt.Sprintf("Acceptance #1625: daily_sales is partitioned by date (%d).", time.Now().UnixNano()),
		"schema_entity",
		[]any{"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"})
	unlinked := captureMemory1625(t, c,
		fmt.Sprintf("Acceptance #1625: revenue is reported net of refunds (%d).", time.Now().UnixNano()),
		"business_knowledge", nil)

	updatedBefore := map[string]time.Time{
		linked:   updatedAt1625(t, memoryRecord1625(t, c, linked)),
		unlinked: updatedAt1625(t, memoryRecord1625(t, c, unlinked)),
	}

	record := awaitVerified1625(t, c, linked)

	if v := memoryRecord1625(t, c, unlinked)["last_verified"]; v != nil && v != "" {
		t.Errorf("last_verified = %v on a record naming no entity; the watcher cannot check one, so it must not stamp it", v)
	}
	for id, before := range updatedBefore {
		after := updatedAt1625(t, memoryRecord1625(t, c, id))
		if !after.Equal(before) {
			t.Errorf("record %s: updated_at moved from %s to %s with no content write", id, before, after)
		}
	}
	if got, _ := record["status"].(string); got != "active" {
		t.Errorf("status = %q after a verification pass, want active", got)
	}
}

// awaitVerified1625 waits for the watcher to stamp one record and returns it.
// The dev stack ticks every 15 seconds, so this covers several ticks and fails
// -- rather than skipping -- when none arrives: a criterion that waits for a
// background pass and then passes because none ran has proved nothing.
func awaitVerified1625(t *testing.T, c *client, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		record := memoryRecord1625(t, c, id)
		if v, _ := record["last_verified"].(string); v != "" {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watcher did not verify entity-linked record %s within 90s; is memory.staleness enabled on this deployment?", id)
		}
		time.Sleep(3 * time.Second)
	}
}
