//go:build integration

package acceptance

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Issue #1773: a table registration reads the whole object it is pointed at --
// by design, since the header row and the line-break scan both need the full
// bytes (#1441) -- and neither builder of the platform's S3 clients passed a
// timeout, so mcp-s3 filled in its 30 s default around the request AND the
// io.ReadAll of the body.
//
// On a full-object read that is not a timeout but a throughput budget. A
// 126 MB CSV registered in 2.05 s on a healthy path and failed at exactly
// 30.0 s, 116 MB in, the day the deployment's DNS sent the pod across a WAN
// link: whether a customer's file could be registered turned on which A record
// the resolver handed out. The instance's documented `timeout` key reached the
// s3_* tools and nothing else, so an operator could not raise it either.
//
// The deadline is now sized from what the platform will actually read: the
// deployment's own upload ceiling, at a throughput floor a store would have to
// fall under to be considered broken -- and an operator's own `timeout` still
// wins outright.
//
// What runs here, and what does not. The criterion below registers a table
// over a CSV at a size the whole read path has to carry, through the streaming
// upload route and the register route a person uses. The degraded link that
// made the old deadline fire is a property of a deployment's network path and
// is not reproducible against a loopback stack: the arithmetic that replaced
// it and the wiring that carries it are covered by TestBlobReadTimeout,
// TestS3Config_Timeout and TestBlobReadCeiling, and the field evidence -- the
// ingress log showing both reads cut at 30.0 s -- is in the ticket.
//
// Wire forms. This ticket touches no MCP tool parameter. Its surfaces are the
// multipart upload route (the file part with a declared Content-Type, which is
// what a browser sends) and the register route's one JSON object, both sent
// below.

const (
	// issue1773Header is the header the file declares, 32 bytes.
	issue1773Header = "store_id,vendor_code,rebate_pct\n"
	// issue1773Row is one record, 30 bytes including the newline. Every record
	// is the same width so the file's size and its row count are each exactly
	// the other, and a query's count is checkable against the bytes uploaded.
	issue1773Row = "10000000,ACME-NW-0001,4.50000\n"
	// issue1773Rows is how many records the file holds, and issue1773Size is
	// what that makes it: ~48 MB, well past the 16 MiB single-PUT bound the
	// streaming upload exists for and far under the dev stack's 250 MB
	// ceiling, so what it exercises is the read rather than either limit.
	issue1773Rows = 1_600_000
	issue1773Size = len(issue1773Header) + issue1773Rows*len(issue1773Row)
)

// TestIssue1773_ACSVTheWholeReadPathCarriesRegisters. Upload at size through
// the route the portal's dialog posts, register through the route its Register
// dialog posts, and query the table: the registration read every byte of the
// object to find the header and to scan for a line break inside a cell, and
// the rows it serves are the rows the file holds.
func TestIssue1773_ACSVTheWholeReadPathCarriesRegisters(t *testing.T) {
	c := connect(t)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "Acceptance 1773 " + stamp

	status, body := send1631(t, c, http.MethodPost, "/api/v1/resources", func(w *multipart.Writer) error {
		return writeSizedCSV1773(w, "acc-1773-"+stamp+".csv", map[string]string{
			"scope":        "global",
			"path":         "acceptance/issue-1773",
			"display_name": name,
			"description":  "Acceptance #1773: a CSV the registration reads whole.",
		})
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("uploading the file: status %d, %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("the upload filed no resource: %v", body)
	}
	t.Cleanup(func() { _, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id, http.NoBody) })

	if size, _ := body["size_bytes"].(float64); int(size) != issue1773Size {
		t.Fatalf("the stored file is %v bytes, not the %d uploaded", body["size_bytes"], issue1773Size)
	}

	table := "acc1773_" + stamp
	status, registered := c.rest(http.MethodPost, "/api/v1/resources/"+id+"/tables",
		jsonBody(t, map[string]any{"connection": scratchResourceConnection, "table_name": table}))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("registering a %d-byte CSV: status %d, %v", issue1773Size, status, registered)
	}
	regID, _ := registered["id"].(string)
	if regID != "" {
		t.Cleanup(func() {
			_, _ = c.rest(http.MethodDelete, "/api/v1/resources/"+id+"/tables/"+regID, http.NoBody)
		})
	}

	queryTable, _ := registered["query_table"].(string)
	if queryTable == "" {
		t.Fatalf("the registration names no query table: %v", registered)
	}
	rows := c.call("trino_query", map[string]any{
		"connection": scratchResourceConnection,
		"sql":        "SELECT count(*) AS n FROM " + queryTable,
		"purpose":    "Acceptance #1773: the table over a CSV read whole serves every row it holds.",
	})
	// Read as a number rather than matched in the rendered result: a count
	// this size comes back through JSON as 1.6e+06.
	if got := countOf1773(t, rows); got != issue1773Rows {
		t.Fatalf("the table serves %d rows, not the file's %d: %v", got, issue1773Rows, rows)
	}
}

// countOf1773 reads the single count a query returned.
func countOf1773(t *testing.T, result map[string]any) int {
	t.Helper()
	list, _ := result["rows"].([]any)
	if len(list) != 1 {
		t.Fatalf("the count query returned %d rows: %v", len(list), result)
	}
	row, _ := list[0].(map[string]any)
	n, ok := row["n"].(float64)
	if !ok {
		t.Fatalf("the count is not a number: %v", result)
	}
	return int(n)
}

// writeSizedCSV1773 streams a CSV of exactly issue1773Size bytes as the file
// part, so the upload costs the test a fixed buffer rather than the whole file
// -- which is also what makes it a real client of a streaming route.
func writeSizedCSV1773(w *multipart.Writer, filename string, fields map[string]string) error {
	for field, value := range fields {
		if err := w.WriteField(field, value); err != nil {
			return err
		}
	}
	part, err := filePart1631(w, filename, "text/csv")
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(issue1773Header)); err != nil {
		return err
	}
	// One buffer of whole records at a time, so no write can ever end partway
	// through one: the remainder is always a multiple of the record width,
	// because the size is defined as a whole number of them.
	const recordsPerChunk = 2048
	chunk := []byte(strings.Repeat(issue1773Row, recordsPerChunk))
	for remaining := issue1773Rows; remaining > 0; {
		n := recordsPerChunk
		if remaining < n {
			n = remaining
		}
		if _, err := part.Write(chunk[:n*len(issue1773Row)]); err != nil {
			return err
		}
		remaining -= n
	}
	return w.Close()
}
