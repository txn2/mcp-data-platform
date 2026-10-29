package epsubmit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
)

func sealer(t *testing.T) epcollect.Sealer {
	t.Helper()
	enc, err := fieldcrypt.NewFieldEncryptor([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	return fieldcrypt.NewRestFieldEncryptor(enc)
}

type sources struct {
	list []whsource.Source
	err  error
}

func (s sources) List(context.Context) ([]whsource.Source, error) { return s.list, s.err }

type tables struct{ err error }

func (t tables) TargetFor(c string) (whtable.Target, error) {
	return whtable.Target{Connection: c, Catalog: "hive", Schema: "scratch"}, t.err
}

type audits struct{ events []middleware.AuditEvent }

func (a *audits) Log(_ context.Context, e middleware.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}

func applySource() whsource.Source {
	return whsource.Source{
		Name: "form-apply", Connection: "scratch",
		Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: "scr_1", Endpoint: "apply"}},
	}
}

// row is a submission as webhook_form_apply holds it: the payload the
// collector wrote, its sensitive field sealed.
func row(t *testing.T, id string, at time.Time, sub epcollect.Submission) map[string]any {
	t.Helper()
	c := &epcollect.Collector{Sealer: sealer(t)}
	sub.Endpoint = "apply"
	_, body, err := c.Event(sub, "form-apply")
	require.NoError(t, err)
	return map[string]any{"event_id": id, "received_at": at.Format("2006-01-02 15:04:05.000000"), "payload": string(body)}
}

func spec(fields ...epform.Field) epform.Spec { return epform.Spec{Fields: fields} }

func reader(t *testing.T, rows []map[string]any, gotSQL *string) (*Reader, *audits) {
	t.Helper()
	a := &audits{}
	return &Reader{
		Sources: sources{list: []whsource.Source{applySource()}}, Tables: tables{}, Sealer: sealer(t), Audit: a,
		Query: func(_ context.Context, connection, sql string) ([]map[string]any, error) {
			assert.Equal(t, "scratch", connection)
			if gotSQL != nil {
				*gotSQL = sql
			}
			return rows, nil
		},
	}, a
}

var (
	name   = epform.Field{Name: "name", Type: epform.TypeText}
	ssn    = epform.Field{Name: "ssn", Type: epform.TypeText, Sensitive: true}
	topics = epform.Field{Name: "topics", Type: epform.TypeMultipleChoice}
)

// #2180: the owner reads the newest submissions with sensitive fields in
// plaintext, and the read is audited with the fields it opened (#2179).
func TestRecent_ReadsAndDecrypts(t *testing.T) {
	at := time.Date(2026, 10, 26, 9, 30, 0, 0, time.UTC)
	rows := []map[string]any{
		row(t, "s2", at, epcollect.Submission{
			Values:    map[string]any{"name": "Bo", "ssn": "987-65-4321", "topics": []any{"a", "b"}},
			Submitter: &epcollect.Submitter{UserID: "u-bo", Email: "bo@example.com"}, Spec: spec(name, ssn, topics),
		}),
		row(t, "s1", at.Add(-time.Hour), epcollect.Submission{
			Values: map[string]any{"name": "Ana", "ssn": "123-45-6789", "old": "x"}, Spec: spec(name, ssn),
		}),
	}
	var sql string
	r, aud := reader(t, rows, &sql)
	page, err := r.Recent(t.Context(), "scr_1", 0, Viewer{UserID: "u-owner", Email: "owner@example.com", Persona: "ops"})
	require.NoError(t, err)
	assert.Equal(t, `SELECT event_id, received_at, payload FROM "hive"."scratch"."webhook_form_apply" ORDER BY received_at DESC LIMIT 50`, sql)
	assert.Equal(t, "apply", page.Form)
	assert.Equal(t, "webhook_form_apply", page.Table)
	assert.Equal(t, []string{"name", "ssn", "topics", "old"}, page.Fields)
	assert.Equal(t, []string{"ssn"}, page.Sensitive)
	require.Len(t, page.Submissions, 2)
	assert.Equal(t, "987-65-4321", page.Submissions[0].Values["ssn"])
	assert.Equal(t, "bo@example.com", page.Submissions[0].Submitter.Email)
	assert.Equal(t, at, page.Submissions[0].ReceivedAt)
	assert.Nil(t, page.Submissions[1].Submitter)

	require.Len(t, aud.events, 1)
	e := aud.events[0]
	assert.Equal(t, "form_decrypt", e.EventKind)
	assert.Equal(t, "owner@example.com", e.UserEmail)
	assert.Equal(t, []string{"ssn"}, e.Parameters["fields"])
	assert.Equal(t, 2, e.Parameters["submissions"])
}

func TestRecent_NothingSensitiveIsNotAudited(t *testing.T) {
	r, aud := reader(t, []map[string]any{row(t, "s1", time.Now(), epcollect.Submission{Values: map[string]any{"name": "Ana"}, Spec: spec(name)})}, nil)
	page, err := r.Recent(t.Context(), "scr_1", MaxLimit+5, Viewer{})
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, page.Limit)
	assert.Equal(t, []string{}, page.Sensitive)
	assert.Empty(t, aud.events)

	none, _ := reader(t, nil, nil)
	empty, err := none.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.NoError(t, err)
	assert.Equal(t, []Submission{}, empty.Submissions, "no submissions is [], never null")
	assert.Equal(t, []string{}, empty.Fields)
}

func TestRecent_Refusals(t *testing.T) {
	r, _ := reader(t, nil, nil)
	_, err := r.Recent(t.Context(), "scr_other", 10, Viewer{})
	require.ErrorIs(t, err, ErrNoForm)

	r.Sources = sources{err: errors.New("db down")}
	_, err = r.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.ErrorContains(t, err, "db down")

	r, _ = reader(t, nil, nil)
	r.Tables = tables{err: errors.New("no scratch catalog")}
	_, err = r.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.ErrorContains(t, err, "no scratch catalog")

	r, _ = reader(t, nil, nil)
	r.Query = func(context.Context, string, string) ([]map[string]any, error) { return nil, errors.New("trino down") }
	_, err = r.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.ErrorContains(t, err, "trino down")

	r, _ = reader(t, []map[string]any{{"event_id": "bad", "payload": "not json"}}, nil)
	_, err = r.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.ErrorContains(t, err, "bad")

	sealed := row(t, "s1", time.Now(), epcollect.Submission{Values: map[string]any{"ssn": "1"}, Spec: spec(ssn)})
	r, _ = reader(t, []map[string]any{sealed}, nil)
	r.Sealer = fieldcrypt.NewRestFieldEncryptor(nil)
	_, err = r.Recent(t.Context(), "scr_1", 10, Viewer{})
	require.ErrorContains(t, err, "ENCRYPTION_KEY")

	assert.False(t, (&Reader{}).Available())
	assert.False(t, (*Reader)(nil).Available())
	assert.True(t, r.Available())
}

func page() Page {
	return Page{
		Source: "form-apply", Fields: []string{"name", "topics", "score"},
		Submissions: []Submission{
			{
				ReceivedAt: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), Submitter: &epcollect.Submitter{Email: "bo@example.com"},
				Values: map[string]any{"name": "=HYPERLINK(\"x\")", "topics": []any{"a", "b"}, "score": 4.5},
			},
			{ReceivedAt: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC), Values: map[string]any{"name": "Ana", "score": json.Number("3")}},
		},
	}
}

// #2180: the export is the rows the page shows, the submitter and each field
// in order, and a CSV cell a spreadsheet reads as a formula is made text.
func TestExport_CSV(t *testing.T) {
	f, err := Export(page(), FormatCSV)
	require.NoError(t, err)
	assert.Equal(t, "text/csv; charset=utf-8", f.ContentType)
	assert.Equal(t, "form-apply-submissions.csv", f.Name)
	lines := strings.Split(strings.TrimSpace(string(f.Data)), "\n")
	assert.Equal(t, "received_at,submitter,name,topics,score", lines[0])
	assert.Equal(t, `2026-10-26T09:00:00Z,bo@example.com,"'=HYPERLINK(""x"")",a; b,4.5`, lines[1])
	assert.Equal(t, "2026-10-26T08:00:00Z,,Ana,,3", lines[2])
}

func TestExport_XLSX(t *testing.T) {
	out, err := Export(page(), FormatXLSX)
	require.NoError(t, err)
	assert.Equal(t, "form-apply-submissions.xlsx", out.Name)
	assert.Contains(t, out.ContentType, "spreadsheetml")
	f, err := excelize.OpenReader(strings.NewReader(string(out.Data)))
	require.NoError(t, err)
	rows, err := f.GetRows("Submissions")
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"received_at", "submitter", "name", "topics", "score"}, rows[0])
	assert.Equal(t, "a; b", rows[1][3])

	_, err = Export(page(), "pdf")
	require.ErrorContains(t, err, "csv or xlsx")
}

func TestTimeOf(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	assert.Equal(t, at, timeOf(at))
	assert.Equal(t, at, timeOf("2026-01-02T03:04:05Z"))
	assert.Equal(t, at, timeOf("2026-01-02 03:04:05"))
	assert.True(t, timeOf(5).IsZero())
	assert.True(t, timeOf("tomorrow").IsZero())
	assert.Empty(t, text(nil))
	assert.Equal(t, "5", text(5))
	assert.Empty(t, cell(nil))
	assert.Equal(t, "true", csvText(true))
	// #2333: a file is exported as its name and the address it was saved at.
	files := []any{
		map[string]any{"name": "logo.png", "uri": "mcp://persona/admin/a/logo.png", "size": 3},
		map[string]any{"name": "unsaved.png"},
	}
	assert.Equal(t, "logo.png <mcp://persona/admin/a/logo.png>; map[name:unsaved.png]", cell(files))
}
