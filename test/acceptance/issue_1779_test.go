//go:build integration

package acceptance

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1779: a CSV record that ends before the header does was treated as a
// defect that made the file uncorrectable, so a Facebook Insights export whose
// exporter omits two trailing columns for one post type -- 21 of its 178
// records -- could not be registered at all. Worse, those records withdrew the
// offer to correct the 148 torn rows the same file had (#1449 makes a file
// uncorrectable while anything is in Ragged), so it had no path forward:
// neither registerable nor correctable.
//
// Those columns are ABSENT, not wrong, and every reader of a CSV supplies
// them: Go's encoding/csv and Python's csv return the short record, PapaParse
// fills the missing keys, Excel and Preview open the file, and the Hive CSV
// reader a registered table is served by reads the values it finds and leaves
// the rest null. Refusing it put this platform alone among them.
//
// What is still refused is the other direction: a record carrying MORE fields
// than the header cannot be trimmed to fit without losing a value the file
// holds.
//
// Wire forms: the bytes reach the platform through `manage_resource` `content`
// and `content_base64` and through the multipart upload route, and `repair` is
// a boolean admitting true, false and absent. Those forms are enumerated and
// sent by the #1774 criteria over the same registrar; what this ticket changes
// is which SHAPES of file are accepted, so the cases below vary the file
// rather than the encoding of the call, and send repair absent and true.

// shortRecords1779 is the shape the exporter writes: a header of three fields,
// and records that stop after the first two because there was nothing to put
// in the third.
const shortRecords1779 = "store_id,vendor_code,rebate_pct\n101,ACME-NW,4.5\n102,BAY\n103,PINE\n"

// shortAndTorn1779 is the file from the ticket in miniature: records that end
// early AND a cell carrying a line break. The line breaks are correctable and
// the short records must not withdraw that offer.
const shortAndTorn1779 = "store_id,address,rebate_pct\n101,\"12 Mill Rd\nSuite 4\",4.5\n102,BAY\n"

// overWide1779 carries the case that is still refused: a torn cell, which is
// correctable, and a record carrying MORE fields than the header, which is
// not. The over-wide record is what withdraws the offer.
//
// An over-wide record with nothing else wrong is not refused and never was
// (#1449: neither condition is a defect on its own), which is why the fixture
// carries both.
const overWide1779 = "store_id,address\n101,\"12 Mill Rd\nSuite 4\"\n102,BAY,6.0,extra\n"

// TestIssue1779_ARecordShortOfTheHeaderRegisters is criterion 1: the file
// registers untouched, no correction is written, and the table serves every
// record including the short ones.
func TestIssue1779_ARecordShortOfTheHeaderRegisters(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content", "1779short"+stamp, []byte(shortRecords1779))

	registered := register1774(t, c, reference, "acc1779short_"+stamp, nil)
	if repaired, _ := registered["repaired"].(string); repaired != "" {
		t.Fatalf("the file was rewritten for a shape that is not a defect: %q", repaired)
	}

	if got := countRows1774(t, c, queryTable1774(t, registered)); got != 3 {
		t.Fatalf("the table serves %d rows, not the file's 3", got)
	}
}

// TestIssue1779_TheAbsentColumnsComeBackEmpty is criterion 2: what a short
// record's missing columns are when the table is queried. Trino reads the
// values it finds and leaves the rest null, which is the whole reason the
// record did not need correcting.
func TestIssue1779_TheAbsentColumnsComeBackEmpty(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content", "1779empty"+stamp, []byte(shortRecords1779))
	registered := register1774(t, c, reference, "acc1779empty_"+stamp, nil)

	result := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql": "SELECT vendor_code, rebate_pct FROM " + queryTable1774(t, registered) +
			" WHERE store_id = '102'",
		"purpose": "Acceptance #1779: the columns a short record does not reach are null rather than wrong.",
	})
	rows, _ := result["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the short record is not in the table: %v", result)
	}
	row, _ := rows[0].(map[string]any)
	if got := fmt.Sprintf("%v", row["vendor_code"]); got != "BAY" {
		t.Fatalf("the value the record DOES carry is %q, not BAY: %v", got, row)
	}
	// The column the record never reached: null, or the empty string the CSV
	// reader gives for a missing trailing field. What it must not be is a
	// value belonging to another column.
	if got := fmt.Sprintf("%v", row["rebate_pct"]); got != "<nil>" && got != "" {
		t.Fatalf("the absent column came back as %q; a short record must not shift its values", got)
	}
}

// TestIssue1779_AShortRecordDoesNotWithdrawTheCorrection is criterion 3 and
// the one the reported file needed: the line breaks are still offered a
// correction, and taking it registers.
func TestIssue1779_AShortRecordDoesNotWithdrawTheCorrection(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content", "1779torn"+stamp, []byte(shortAndTorn1779))

	_, text, err := c.callRaw("manage_table", registerArgs1774(reference, "acc1779torn_"+stamp, nil))
	if err != nil {
		t.Fatalf("manage_table register: transport error: %v", err)
	}
	if !strings.Contains(text, "line break inside a cell") {
		t.Fatalf("the refusal does not name the line breaks: %s", text)
	}
	if !strings.Contains(text, "asking for the file to be corrected") {
		t.Fatalf("the short record withdrew the correction offer (#1779): %s", text)
	}
	if strings.Contains(text, "header's 3 fields") {
		t.Fatalf("a record short of the header is still reported as a defect: %s", text)
	}

	repair := true
	registered := register1774(t, c, reference, "acc1779torn_"+stamp, &repair)
	if repaired, _ := registered["repaired"].(string); !strings.Contains(repaired, "put 1 row back onto one line") {
		t.Fatalf("the correction did not happen: %v", registered)
	}
	if got := countRows1774(t, c, queryTable1774(t, registered)); got != 2 {
		t.Fatalf("the corrected table serves %d rows, not the file's 2", got)
	}
}

// TestIssue1779_ARecordWiderThanTheHeaderStillWithdrawsTheOffer is criterion
// 4: the direction that does lose data is unchanged. It withdraws the
// correction the same file's torn cell would otherwise be offered, and the
// refusal says so in those terms.
func TestIssue1779_ARecordWiderThanTheHeaderStillWithdrawsTheOffer(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	reference, _ := file1774(t, c, "content", "1779wide"+stamp, []byte(overWide1779))

	for _, repair := range []*bool{nil, boolPtr1774(true)} {
		_, text, err := c.callRaw("manage_table", registerArgs1774(reference, "acc1779wide_"+stamp, repair))
		if err != nil {
			t.Fatalf("manage_table register: transport error: %v", err)
		}
		if !strings.Contains(text, "more than the header's 2 fields") {
			t.Fatalf("the refusal does not name the over-wide record: %s", text)
		}
		if !strings.Contains(text, "record 2 has 4") {
			t.Fatalf("the refusal does not name which record: %s", text)
		}
		if strings.Contains(text, "asking for the file to be corrected") {
			t.Fatalf("a correction is offered that would then decline: %s", text)
		}
	}
}

// TestIssue1779_TheViewerReportsTheShapeItParsed is criterion 5, read through
// the REST route the portal's CSV viewer loads its content from: the file the
// viewer draws is the one whose shape it now states, so the two cannot
// disagree the way they did when parsed.errors was discarded.
func TestIssue1779_TheViewerReportsTheShapeItParsed(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	_, id := file1774(t, c, "content", "1779view"+stamp, []byte(shortRecords1779))

	served := resourceContent1775(t, c, id)
	if served != shortRecords1779 {
		t.Fatalf("the viewer is served different bytes than were stored:\n%q", served)
	}
	// Two of the three records end before the header does, which is what the
	// viewer's note counts. Asserted here on the bytes because the note itself
	// is drawn in the browser; CsvRenderer.test.tsx pins the sentence.
	short := 0
	for _, line := range strings.Split(strings.TrimSpace(served), "\n")[1:] {
		if strings.Count(line, ",") < 2 {
			short++
		}
	}
	if short != 2 {
		t.Fatalf("the fixture does not carry the shape this criterion is about: %d short records", short)
	}
	status, body := c.rest(http.MethodGet, "/api/v1/resources/"+id, http.NoBody)
	if status != http.StatusOK {
		t.Fatalf("reading the resource: status %d, %v", status, body)
	}
}
