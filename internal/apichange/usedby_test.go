package apichange

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestOperationIndex_Resolve(t *testing.T) {
	idx := NewOperationIndex(specV1, "not a spec")
	op, ok := idx.Resolve("listPets", "", "")
	require.True(t, ok)
	assert.Equal(t, "GET", op.Method)
	_, ok = idx.Resolve("nope", "", "")
	assert.False(t, ok)

	op, ok = idx.Resolve("", "get", "/pets/42?x=1")
	require.True(t, ok, "a concrete path matches its template")
	assert.Equal(t, "getPet", op.ID)
	op, ok = idx.Resolve("", "DELETE", "/v2/api/pets/42")
	require.True(t, ok, "a base-path prefix the spec does not write is skipped")
	assert.Equal(t, "deletePet", op.ID)
	op, ok = idx.Resolve("", "GET", "/health")
	require.True(t, ok)
	assert.Equal(t, "GET /health", op.ID)
	_, ok = idx.Resolve("", "PUT", "/pets/42")
	assert.False(t, ok)

	literal := NewOperationIndex(`openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /pets/{id}:
    get:
      operationId: getPet
      parameters: [{name: id, in: path, required: true, schema: {type: string}}]
      responses: {"200": {description: ok}}
  /pets/mine:
    get: {operationId: myPets, responses: {"200": {description: ok}}}
  /{kind}/{id}:
    get:
      operationId: anything
      parameters:
        - {name: kind, in: path, required: true, schema: {type: string}}
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {"200": {description: ok}}
`)
	op, _ = literal.Resolve("", "GET", "/pets/mine")
	assert.Equal(t, "myPets", op.ID, "the literal path wins")
	op, _ = literal.Resolve("", "GET", "/pets/7")
	assert.Equal(t, "getPet", op.ID, "the more literal template wins")
	assert.Len(t, literal.Ops(), 3)
}

// scriptSource calls listPets by id and deletePet by path on "vendor", and
// something on a connection the index is not asked about.
const scriptSource = `def main():
    platform.call("api_invoke_endpoint", {"connection": "vendor", "operation_id": "listPets"})
    platform.call("api_export", {"connection": "vendor", "method": "DELETE", "path": "/pets/7", "name": "x"})
    platform.call("api_invoke_endpoint", {"connection": "other", "operation_id": "listPets"})
`

var (
	scriptCols = []string{"id", "name", "source_code", "endpoint"}
	runCols    = []string{"operation", "connection", "id", "name", "endpoint", "last_seen_at"}
	promptCols = []string{"id", "name", "content"}
)

// #2485: what uses each operation, from scripts' sources, runs and prompts.
func TestUsage_Callers(t *testing.T) {
	db, mock := mockDB(t)
	u := Usage{DB: db}
	seen := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows(scriptCols).
		AddRow("s1", "nightly", scriptSource, "pets-hook").
		AddRow("s2", "broken", "def main(:\n", ""))
	mock.ExpectQuery(regexp.QuoteMeta(runCallersSQL)).WillReturnRows(sqlmock.NewRows(runCols).
		AddRow("getPet", "vendor", "s3", "computed", "", seen))
	mock.ExpectQuery(regexp.QuoteMeta(promptsSQL)).WillReturnRows(sqlmock.NewRows(promptCols).
		AddRow("p1", "triage", "On vendor, call getPet first; listPetsExtra is unrelated.").
		AddRow("p2", "other", "vendor"))

	got, err := u.Callers(context.Background(), []string{"vendor"}, NewOperationIndex(specV1))
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, []Caller{
		{Kind: CallerEndpoint, ID: "pets-hook", Name: "pets-hook", Via: ViaSource, Connection: "vendor"},
		{Kind: CallerScript, ID: "s1", Name: "nightly", Via: ViaSource, Connection: "vendor"},
	}, got["listPets"])
	assert.Equal(t, []Caller{
		{Kind: CallerEndpoint, ID: "pets-hook", Name: "pets-hook", Via: ViaSource, Connection: "vendor"},
		{Kind: CallerScript, ID: "s1", Name: "nightly", Via: ViaSource, Connection: "vendor"},
	}, got["deletePet"], "a path call resolves to its operation")
	require.Len(t, got["getPet"], 2)
	assert.Equal(t, Caller{Kind: CallerPrompt, ID: "p1", Name: "triage", Via: ViaPrompt, Connection: "vendor"}, got["getPet"][0])
	assert.Equal(t, CallerScript, got["getPet"][1].Kind)
	assert.Equal(t, ViaRun, got["getPet"][1].Via)
	assert.Equal(t, seen, *got["getPet"][1].LastSeenAt)
	assert.NotContains(t, got, "createPet")
}

func TestUsage_CallersFailures(t *testing.T) {
	empty, err := (Usage{}).Callers(context.Background(), []string{"vendor"}, NewOperationIndex(specV1))
	require.NoError(t, err)
	assert.Empty(t, empty)

	for name, setup := range map[string]func(sqlmock.Sqlmock){
		"scripts": func(m sqlmock.Sqlmock) { m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnError(errDown) },
		"runs": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows(scriptCols))
			m.ExpectQuery(regexp.QuoteMeta(runCallersSQL)).WillReturnError(errDown)
		},
		"prompts": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows(scriptCols))
			m.ExpectQuery(regexp.QuoteMeta(runCallersSQL)).WillReturnRows(sqlmock.NewRows(runCols))
			m.ExpectQuery(regexp.QuoteMeta(promptsSQL)).WillReturnError(errDown)
		},
		"a script row": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("s"))
		},
		"a run row": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows(scriptCols))
			m.ExpectQuery(regexp.QuoteMeta(runCallersSQL)).WillReturnRows(sqlmock.NewRows([]string{"op"}).AddRow("x"))
		},
		"a prompt row": func(m sqlmock.Sqlmock) {
			m.ExpectQuery(regexp.QuoteMeta(scriptsSQL)).WillReturnRows(sqlmock.NewRows(scriptCols))
			m.ExpectQuery(regexp.QuoteMeta(runCallersSQL)).WillReturnRows(sqlmock.NewRows(runCols))
			m.ExpectQuery(regexp.QuoteMeta(promptsSQL)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("p"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			db, mock := mockDB(t)
			setup(mock)
			_, err := (Usage{DB: db}).Callers(context.Background(), []string{"vendor"}, NewOperationIndex(specV1))
			require.Error(t, err)
		})
	}
}

// recordingSender records what a notifier posted.
type recordingSender struct {
	names []string
	key   string
	p     notification.Payload
	calls int
}

func (r *recordingSender) Send(_ context.Context, names []string, key string, p notification.Payload) int {
	r.names, r.key, r.p = names, key, p
	r.calls++
	return len(names)
}

func TestChangeBody_EveryValueIsACodeSpan(t *testing.T) {
	c := Change{Status: StatusPending, Report: Report{
		FromVersion: "1", ToVersion: "2",
		Removed: []Op{{
			ID: "deletePet", Method: "DELETE", Path: "/pets/{id}",
			UsedBy: []Caller{{Kind: CallerScript, Name: "night`ly"}, {Kind: CallerScript, Name: "night`ly", Via: ViaRun}},
		}},
		Changed:    []Changed{{Op{ID: "GET /health", Method: "GET", Path: "/health"}, []string{PartResponses}}},
		Deprecated: []Op{{ID: "getPet", Summary: "<b>Get</b>"}},
		Added:      []Op{{ID: "createPet"}},
	}}
	body := ChangeBody(c)
	assert.Contains(t, body, "Waiting for review")
	assert.Contains(t, body, "- `deletePet` `DELETE /pets/{id}`; used by script `night'ly`\n", "one line per caller, backticks neutralized")
	assert.Contains(t, body, "- `GET /health`: `responses`")
	assert.Contains(t, body, "`<b>Get</b>`")
	for _, s := range []string{StatusApplied, StatusReported, StatusDiscarded} {
		assert.NotEmpty(t, statusLabel(s))
	}

	many := Change{Report: Report{Added: make([]Op, maxNotified+3)}}
	assert.Contains(t, ChangeBody(many), "- and 3 more")
}

func TestChannelNotifier(t *testing.T) {
	db, mock := mockDB(t)
	s := &recordingSender{}
	n := &ChannelNotifier{Settings: NewSettings(db), Sender: s, PortalBase: "https://portal.example/"}

	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}))
	n.Changed(t.Context(), Change{ID: "c1"})
	assert.Zero(t, s.calls, "nothing is sent while no channel is named")

	settingsRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow([]byte(`{"channels":["ops"]}`), "a", time.Now())
	}
	mock.ExpectQuery("FROM platform_settings").WillReturnRows(settingsRow())
	n.Changed(t.Context(), Change{ID: "c1", CatalogID: "acme", SpecName: "pets", Status: StatusApplied})
	assert.Equal(t, []string{"ops"}, s.names)
	assert.Equal(t, "api_change:c1", s.key)
	assert.Equal(t, "API spec changed: acme/pets", s.p.Document.Title)
	assert.Equal(t, "https://portal.example/portal/admin/api-catalogs", s.p.Link)

	mock.ExpectQuery("FROM platform_settings").WillReturnRows(settingsRow())
	n.Warned(t.Context(), Warning{Connection: "vendor", Operation: "getPet", Kind: WarningSunset, Detail: "2027"})
	assert.Equal(t, "API upstream warning: vendor Sunset", s.p.Document.Title)
	assert.True(t, strings.HasPrefix(s.p.Document.Body, "Sunset on connection `vendor`: `getPet`"))
	assert.Equal(t, "Deprecation", warningLabel(WarningDeprecation))
	assert.Equal(t, "Removed", warningLabel(WarningRemoved))

	mock.ExpectQuery("FROM platform_settings").WillReturnError(errDown)
	n.Warned(t.Context(), Warning{Connection: "vendor"})
	assert.Equal(t, 2, s.calls, "a settings read that fails sends nothing")

	var none *ChannelNotifier
	none.Changed(t.Context(), Change{})

	mock.ExpectQuery("FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
		AddRow([]byte(`{}`), "a", time.Now()))
	got, err := ReadSettings(t.Context(), NewSettings(db))
	require.NoError(t, err)
	assert.Equal(t, []string{}, got.Channels, "an empty list is [], never null")
}
