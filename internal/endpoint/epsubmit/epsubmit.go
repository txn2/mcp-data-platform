// Package epsubmit reads a form's stored submissions for its owner (#2180):
// the newest rows of webhook_form_<endpoint>, through the Trino connection
// the form's source is on, with each sensitive field decrypted and the read
// audited with the fields it opened (#2179), and the same rows as CSV or an
// Excel workbook.
package epsubmit

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/tablexlsx"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// Bounds of a read.
const (
	// DefaultLimit is how many submissions the page shows.
	DefaultLimit = 50
	// MaxLimit is the most one read or export returns.
	MaxLimit = 10000
	// maxExportBytes bounds an export's workbook.
	maxExportBytes = 64 << 20
)

// The export formats.
const (
	FormatCSV  = "csv"
	FormatXLSX = "xlsx"
)

// floatBits is the size of the float a number is written from.
const floatBits = 64

// The columns every export starts with.
const (
	columnReceived  = "received_at"
	columnSubmitter = "submitter"
)

// ErrNoForm is a script with no form source: it was never bound as a form.
var ErrNoForm = errors.New("this script stores no form submissions: bind it as a form with manage_script endpoint_set")

// Viewer is who reads the submissions.
type Viewer struct {
	UserID  string
	Email   string
	Persona string
}

// Reader reads a form's submissions.
type Reader struct {
	Sources epcollect.SourceLister
	Tables  Targets
	Query   Querier
	Sealer  epcollect.Sealer
	Audit   middleware.AuditLogger
}

// Submission is one stored submission.
type Submission struct {
	ID         string               `json:"id"`
	ReceivedAt time.Time            `json:"received_at"`
	Submitter  *epcollect.Submitter `json:"submitter,omitempty"`
	Values     map[string]any       `json:"values"`
}

// Page is a form's newest submissions and what reads them.
type Page struct {
	Form       string `json:"form"`
	Source     string `json:"source"`
	Table      string `json:"table"`
	Connection string `json:"connection"`
	// Fields are the columns, in the order the newest submission's form
	// declared them, then any an older one held.
	Fields []string `json:"fields"`
	// Sensitive are the fields that were decrypted for this read.
	Sensitive   []string     `json:"sensitive"`
	Submissions []Submission `json:"submissions"`
	Limit       int          `json:"limit"`
}

// Available reports whether the reader has what a read needs.
func (r *Reader) Available() bool {
	return r != nil && r.Sources != nil && r.Tables != nil && r.Query != nil
}

// Recent is the newest limit submissions of the form scriptID is, with each
// sensitive field decrypted for who, and the read audited when it decrypted
// any.
func (r *Reader) Recent(ctx context.Context, scriptID string, limit int, who Viewer) (Page, error) {
	src, err := r.source(ctx, scriptID)
	if err != nil {
		return Page{}, err
	}
	tg, err := r.Tables.TargetFor(src.Connection)
	if err != nil {
		return Page{}, fmt.Errorf("the form's connection %s: %w", src.Connection, err)
	}
	limit = clamp(limit)
	rows, err := r.Query(ctx, src.Connection, selectRecent(tg, src.TableName(), limit))
	if err != nil {
		return Page{}, fmt.Errorf("reading %s: %w", src.TableName(), err)
	}
	page := Page{
		Form: src.Config.Form.Endpoint, Source: src.Name, Table: src.TableName(), Connection: src.Connection,
		Fields: []string{}, Sensitive: []string{}, Submissions: make([]Submission, 0, len(rows)), Limit: limit,
	}
	opened := map[string]bool{}
	for _, row := range rows {
		got, err := r.read(row)
		if err != nil {
			return Page{}, err
		}
		page.Submissions = append(page.Submissions, got.sub)
		page.Fields = appendNew(page.Fields, got.fields...)
		for _, s := range got.sensitive {
			opened[s] = true
		}
	}
	for _, f := range page.Fields {
		if opened[f] {
			page.Sensitive = append(page.Sensitive, f)
		}
	}
	if len(page.Sensitive) > 0 {
		r.audit(ctx, who, src, page)
	}
	return page, nil
}

// source is the form source made for scriptID.
func (r *Reader) source(ctx context.Context, scriptID string) (whsource.Source, error) {
	list, err := r.Sources.List(ctx)
	if err != nil {
		return whsource.Source{}, fmt.Errorf("listing webhook sources: %w", err)
	}
	for _, s := range list {
		if s.IsForm() && s.Config.Form != nil && s.Config.Form.ScriptID == scriptID {
			return s, nil
		}
	}
	return whsource.Source{}, ErrNoForm
}

// readRow is one row as a submission, its fields in order and the sensitive
// fields decrypted for it.
type readRow struct {
	sub       Submission
	fields    []string
	sensitive []string
}

// read is one row as a submission.
func (r *Reader) read(row map[string]any) (readRow, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(text(row["payload"])), &payload); err != nil {
		return readRow{}, fmt.Errorf("reading submission %s: %w", text(row["event_id"]), err)
	}
	values, sensitive := epcollect.PayloadValues(payload)
	values, err := epcollect.Reveal(r.Sealer, values, sensitive)
	if err != nil {
		return readRow{}, fmt.Errorf("submission %s: %w", text(row["event_id"]), err)
	}
	sub := Submission{ID: text(row["event_id"]), ReceivedAt: timeOf(row["received_at"]), Values: values}
	if raw, ok := payload[epcollect.KeySubmitter].(map[string]any); ok {
		sub.Submitter = &epcollect.Submitter{UserID: text(raw["user_id"]), Email: text(raw["email"]), AuthType: text(raw["auth_type"])}
	}
	fields := nameList(payload[epcollect.KeyFields])
	for name := range values {
		fields = appendNew(fields, name)
	}
	return readRow{sub: sub, fields: fields, sensitive: sensitive}, nil
}

// audit records that who read the page's sensitive fields in plaintext.
func (r *Reader) audit(ctx context.Context, who Viewer, src whsource.Source, page Page) {
	if r.Audit == nil {
		return
	}
	event := middleware.AuditEvent{
		Timestamp: time.Now().UTC(), UserID: who.UserID, UserEmail: who.Email, Persona: who.Persona,
		ToolName: "form_submissions", Success: true, Authorized: true, Source: "portal",
		EventKind: string(audit.EventTypeFormDecrypt), Transport: "http",
		Parameters: map[string]any{
			"form": page.Form, "source": src.Name, "fields": page.Sensitive, "submissions": len(page.Submissions),
			"script_id": src.Config.Form.ScriptID,
		},
	}
	if err := r.Audit.Log(ctx, event); err != nil {
		slog.WarnContext(ctx, "forms: recording a submissions read failed", "form", logsan.SanitizeForLog(src.Name),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// File is an export: its bytes, content type and file name.
type File struct {
	Data        []byte
	ContentType string
	Name        string
}

// Export is the page's submissions as a file.
func Export(page Page, format string) (File, error) {
	header := append([]string{columnReceived, columnSubmitter}, page.Fields...)
	rows := make([][]any, 0, len(page.Submissions))
	for _, s := range page.Submissions {
		row := make([]any, 0, len(header))
		submitter := ""
		if s.Submitter != nil {
			submitter = s.Submitter.Email
		}
		row = append(row, s.ReceivedAt.UTC().Format(time.RFC3339), submitter)
		for _, f := range page.Fields {
			row = append(row, cell(s.Values[f]))
		}
		rows = append(rows, row)
	}
	base := page.Source + "-submissions"
	switch format {
	case FormatCSV:
		data, err := writeCSV(header, rows)
		return File{Data: data, ContentType: "text/csv; charset=utf-8", Name: base + ".csv"}, err
	case FormatXLSX:
		wb := tablexlsx.Workbook{Sheets: []tablexlsx.Sheet{{Name: "Submissions", Columns: header, Rows: rows, Freeze: "A2"}}}
		data, err := wb.Write(maxExportBytes)
		if err != nil {
			return File{}, fmt.Errorf("writing the workbook: %w", err)
		}
		return File{Data: data, ContentType: tablexlsx.ContentType, Name: base + tablexlsx.Extension}, nil
	}
	return File{}, fmt.Errorf("an export is %s or %s, not %q", FormatCSV, FormatXLSX, format)
}

func writeCSV(header []string, rows [][]any) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, fmt.Errorf("writing the export: %w", err)
	}
	for _, row := range rows {
		rec := make([]string, len(row))
		for i, v := range row {
			rec[i] = csvText(v)
		}
		if err := w.Write(rec); err != nil {
			return nil, fmt.Errorf("writing the export: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("writing the export: %w", err)
	}
	return buf.Bytes(), nil
}

// cell is a value as an export cell: a list of choices joined, each file a
// submission carried (#2333) as its name and the address it was saved at,
// everything else as it is.
func cell(v any) any {
	switch t := v.(type) {
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, itemText(item))
		}
		return strings.Join(parts, "; ")
	case nil:
		return ""
	}
	return v
}

// itemText is one item of a list value: a saved file's name and address, or
// the item as it prints.
func itemText(item any) string {
	if f, ok := item.(map[string]any); ok {
		if uri, ok := f[epform.FileKeyURI].(string); ok {
			return fmt.Sprintf("%v <%s>", f[epform.FileKeyName], uri)
		}
	}
	return fmt.Sprint(item)
}

// csvText is a cell as CSV text. A value a spreadsheet would read as a
// formula is prefixed with an apostrophe, so opening the file runs nothing a
// submitter typed.
func csvText(v any) string {
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case float64:
		s = strconv.FormatFloat(t, 'f', -1, floatBits)
	default:
		s = fmt.Sprint(t)
	}
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func selectRecent(tg whtable.Target, table string, limit int) string {
	return fmt.Sprintf(`SELECT event_id, received_at, payload FROM %s.%s.%s ORDER BY received_at DESC LIMIT %d`,
		quoteIdent(tg.Catalog), quoteIdent(tg.Schema), quoteIdent(table), limit)
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func clamp(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	}
	return limit
}

func appendNew(list []string, names ...string) []string {
	for _, n := range names {
		if !slices.Contains(list, n) {
			list = append(list, n)
		}
	}
	return list
}

func text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return fmt.Sprint(v)
}

// timeOf reads a timestamp column, a time or the text Trino renders one as.
func timeOf(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05.999999 UTC", "2006-01-02 15:04:05"} {
			if at, err := time.Parse(layout, t); err == nil {
				return at.UTC()
			}
		}
	}
	return time.Time{}
}

// nameList reads a payload's list of field names.
func nameList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
