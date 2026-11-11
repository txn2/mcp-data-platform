package generalfeedback

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

func TestSeed(t *testing.T) {
	assert.Equal(t, Settings{Personas: []string{"admin"}, Recipients: []string{}, Channels: []string{}},
		Seed(nil, "admin"), "nothing configured seeds the administrator persona")
	got := Seed([]string{"persona:data-eng", " Requests@Example.com ", "channel:data-team", "requests@example.com", "", "persona:data-eng"}, "admin")
	assert.Equal(t, []string{"data-eng"}, got.Personas, "an entry list replaces the default")
	assert.Equal(t, []string{"requests@example.com"}, got.Recipients, "addresses compared and deduplicated")
	assert.Equal(t, []string{"data-team"}, got.Channels)
	assert.False(t, got.AcknowledgeAuthor)
}

func members(people map[string][]string) func(context.Context, string) []string {
	return func(_ context.Context, p string) []string { return people[p] }
}

func options() []PersonaOption {
	return []PersonaOption{{Name: "admin", DisplayName: "Administrator"}, {Name: "analyst", DisplayName: "Data Analyst"}}
}

// The people a list reaches are the personas' members and the typed
// addresses, once each.
func TestRecipients(t *testing.T) {
	svc := New(Deps{
		Seed:     Settings{Personas: []string{"admin", "analyst"}, Recipients: []string{"team@b.io", "ann@b.io"}, Channels: []string{"ops"}, AcknowledgeAuthor: true},
		Members:  members(map[string][]string{"admin": {"ann@b.io", "bo@b.io"}, "analyst": {"Bo@B.io"}}),
		Personas: options,
	})
	people, channels, ack := svc.Recipients(context.Background())
	assert.Equal(t, []string{"ann@b.io", "bo@b.io", "team@b.io"}, people)
	assert.Equal(t, []string{"ops"}, channels)
	assert.True(t, ack)
	assert.True(t, svc.ReachesSomeone(context.Background()))
}

// TestView_WarnsWhenTheListReachesNobody: an administrator persona nobody
// with an address belongs to reaches nobody, and the view says so with each
// persona's count; lists are [] rather than null.
func TestView_WarnsWhenTheListReachesNobody(t *testing.T) {
	svc := New(Deps{
		Seed:     Settings{Personas: []string{"admin"}},
		Members:  members(map[string][]string{"analyst": {"a@b.io", "b@b.io"}}),
		Personas: options,
	})
	v := svc.View(context.Background())
	assert.Equal(t, []string{WarningReachesNobody}, v.Warnings)
	assert.Equal(t, []string{}, v.People)
	assert.Equal(t, []string{}, v.Recipients)
	assert.Equal(t, []PersonaOption{{Name: "admin", DisplayName: "Administrator"}, {Name: "analyst", DisplayName: "Data Analyst", People: 2}}, v.PersonaOptions)
	assert.False(t, svc.ReachesSomeone(context.Background()))

	raw, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "null")
}

func manyAddresses(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "person" + strconv.Itoa(i) + "@b.io"
	}
	return out
}

func sectionOver(t *testing.T) (*settingsjson.Section[Settings], sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return settingsjson.New[Settings](db, SettingsSection), mock
}

// TestSave_RefusesWhatTheListCannotHold: an unknown persona, an entry that is
// not an address, a channel with no channel store, and too many entries are
// each refused naming what is wrong; nothing is written.
func TestSave_RefusesWhatTheListCannotHold(t *testing.T) {
	section, mock := sectionOver(t)
	svc := New(Deps{Section: section, Personas: options})
	for _, tc := range []struct {
		in   Settings
		says string
	}{
		{Settings{Personas: []string{"ghost"}}, `no persona is named "ghost"`},
		{Settings{Recipients: []string{"not an address"}}, "is not an email address"},
		{Settings{Recipients: []string{"@b.io"}}, "is not an email address"},
		{Settings{Channels: []string{"data-team"}}, "channels:"},
		{Settings{Recipients: manyAddresses(maxRecipients + 1)}, "at most"},
	} {
		_, err := svc.Save(context.Background(), tc.in, "admin@b.io")
		var invalid *InvalidError
		if !errors.As(err, &invalid) || !strings.Contains(invalid.Error(), tc.says) {
			t.Errorf("%+v: %v; want a refusal saying %q", tc.in, err, tc.says)
		}
	}
	require.NoError(t, mock.ExpectationsWereMet(), "nothing was written")

	_, err := New(Deps{Personas: options}).Save(context.Background(), Settings{}, "admin@b.io")
	assert.ErrorIs(t, err, ErrNoStore)
}

// TestRoutes: GET answers the seed with its warnings; PUT stores a list and
// answers it; a refused list is 400; a body that is not a list is 400; the
// portal read says only whether anyone is told.
func TestRoutes(t *testing.T) {
	section, mock := sectionOver(t)
	svc := New(Deps{
		Section: section, Seed: Seed(nil, "admin"), Personas: options,
		Members: members(map[string][]string{"admin": {"ann@b.io"}}),
	})
	mux := http.NewServeMux()
	pass := func(h http.Handler) http.Handler { return h }
	RegisterSettings(mux, pass, svc, func(*http.Request) string { return "admin@b.io" })
	RegisterReach(mux, pass, svc)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
		return rec
	}

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}))
	got := do(http.MethodGet, settingsPath, "")
	require.Equal(t, http.StatusOK, got.Code)
	var view View
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &view))
	assert.Equal(t, []string{"admin"}, view.Personas)
	assert.Equal(t, []string{"ann@b.io"}, view.People)

	saved, _ := json.Marshal(Settings{Personas: []string{"analyst"}, Recipients: []string{"team@b.io"}, AcknowledgeAuthor: true})
	mock.ExpectExec("INSERT INTO platform_settings").WithArgs(SettingsSection, sqlmock.AnyArg(), "admin@b.io").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(saved, "admin@b.io", time.Now()))
	got = do(http.MethodPut, settingsPath, `{"personas":["analyst"],"recipients":["Team@B.io"],"channels":[],"acknowledge_author":true}`)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	require.NoError(t, json.Unmarshal(got.Body.Bytes(), &view))
	assert.Equal(t, []string{"team@b.io"}, view.People)
	assert.Equal(t, "admin@b.io", view.UpdatedBy)
	assert.True(t, view.AcknowledgeAuthor)

	assert.Equal(t, http.StatusBadRequest, do(http.MethodPut, settingsPath, `{"personas":["ghost"]}`).Code)
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPut, settingsPath, `{"persona":"admin"}`).Code, "an unknown field")
	assert.Equal(t, http.StatusBadRequest, do(http.MethodPut, settingsPath, `nope`).Code)

	mock.ExpectQuery("SELECT value, updated_by, updated_at FROM platform_settings").WillReturnRows(
		sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).AddRow(saved, "admin@b.io", time.Now()))
	got = do(http.MethodGet, reachPath, "")
	assert.JSONEq(t, `{"reaches_someone":true}`, got.Body.String())
	require.NoError(t, mock.ExpectationsWereMet())
}

// A store that fails answers 500 without repeating the failure.
func TestRoutes_AStoreFailureIs500(t *testing.T) {
	section, mock := sectionOver(t)
	svc := New(Deps{Section: section, Personas: options})
	mux := http.NewServeMux()
	RegisterSettings(mux, func(h http.Handler) http.Handler { return h }, svc, func(*http.Request) string { return "a@b.io" })
	mock.ExpectExec("INSERT INTO platform_settings").WillReturnError(errors.New("connection reset by 10.0.0.5"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, settingsPath, strings.NewReader(`{"personas":["admin"]}`)))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.5")

	none := http.NewServeMux()
	RegisterSettings(none, func(h http.Handler) http.Handler { return h }, New(Deps{Personas: options}), func(*http.Request) string { return "" })
	rec = httptest.NewRecorder()
	none.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPut, settingsPath, strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusConflict, rec.Code)
}
