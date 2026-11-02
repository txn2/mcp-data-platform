package embedhosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	got, err := Normalize([]string{
		" Fast.Wistia.NET ", "https://cdn.example.com/", "https://*.cloudfront.net",
		"https://media.example.com:8443", "fast.wistia.net",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"https://fast.wistia.net", "https://cdn.example.com", "https://*.cloudfront.net",
		"https://media.example.com:8443",
	}, got, "lower case, https, no trailing slash, duplicates dropped, order kept")

	empty, err := Normalize([]string{})
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty, "an empty list turns embeds off and is stored as []")
}

// TestNormalize_RefusesWhatCouldWidenOrBreakThePolicy holds the entry to a
// policy host source: each of these would end a directive, add one, or allow
// more than one host.
func TestNormalize_RefusesWhatCouldWidenOrBreakThePolicy(t *testing.T) {
	for _, entry := range []string{
		"*", "https://*", "http://cdn.example.com", "data:", "'self'", "'unsafe-inline'",
		"cdn.example.com; script-src *", "cdn.example.com,other.com", "cdn example.com",
		"https://cdn.example.com/path", "https://cdn.example.com?x=1", "localhost",
		"https://cdn.example.com:0", "https://cdn.example.com:99999", "https://cdn.*.com",
		"\"cdn.example.com\"", "", "com", "*.com", "javascript://x.y",
	} {
		_, err := Normalize([]string{entry})
		require.Error(t, err, "%q", entry)
		assert.ErrorIs(t, err, ErrInvalid)
	}
	_, err := Normalize(make([]string, MaxHosts+1))
	require.ErrorIs(t, err, ErrInvalid)
}

func TestConfig(t *testing.T) {
	assert.Equal(t, Default, Config{}.Configured(), "unset takes the default")
	assert.NoError(t, Config{}.Validate())

	none := []string{}
	assert.Empty(t, Config{Hosts: &none}.Configured(), "an empty list turns embeds off")

	own := []string{"cdn.example.com"}
	assert.Equal(t, []string{"https://cdn.example.com"}, Config{Hosts: &own}.Configured())

	bad := []string{"cdn.example.com; x"}
	err := Config{Hosts: &bad}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "portal.embeds.hosts")
	assert.Equal(t, Default, Config{Hosts: &bad}.Configured())
}

func TestSources(t *testing.T) {
	assert.Equal(t, "https://a.example.com https://b.example.com", Sources([]string{"a.example.com", "https://b.example.com"}))
	assert.Empty(t, Sources(nil))
	assert.Empty(t, Sources([]string{"ok.example.com", "bad; x"}), "an invalid list adds nothing")
}

func TestDefaults_IsACopy(t *testing.T) {
	d := Defaults()
	d[0] = "changed"
	assert.NotEqual(t, "changed", Default[0])
}

// memSection is the stored section in memory.
type memSection struct {
	value   Settings
	written bool
	readErr error
	reads   int
}

func (m *memSection) Read(context.Context) (Settings, bool, error) {
	m.reads++
	return m.value, m.written, m.readErr
}

func (m *memSection) Write(_ context.Context, s Settings, _ string) error {
	if m.readErr != nil {
		return m.readErr
	}
	m.value, m.written = s, true
	return nil
}

func TestResolver(t *testing.T) {
	ctx := context.Background()
	own := []string{"cdn.example.com"}
	sec := &memSection{}
	r := NewResolver(sec, Config{Hosts: &own})
	clock := time.Unix(1700000000, 0)
	r.now = func() time.Time { return clock }

	assert.Equal(t, []string{"https://cdn.example.com"}, r.Hosts(ctx), "nothing written: the configured list")

	sec.value, sec.written = Settings{Hosts: []string{"https://fast.wistia.net"}}, true
	assert.Equal(t, []string{"https://cdn.example.com"}, r.Hosts(ctx), "served from the cache")
	clock = clock.Add(cacheFor)
	assert.Equal(t, []string{"https://fast.wistia.net"}, r.Hosts(ctx), "another replica's write is read after the cache")

	sec.value = Settings{Hosts: []string{"bad; x"}}
	clock = clock.Add(cacheFor)
	assert.Equal(t, []string{"https://cdn.example.com"}, r.Hosts(ctx), "an invalid stored list is not used")

	sec.readErr = errors.New("down")
	clock = clock.Add(cacheFor)
	assert.Equal(t, []string{"https://cdn.example.com"}, r.Hosts(ctx), "a failed read serves the configured list")

	assert.Equal(t, Default, NewResolver(nil, Config{}).Hosts(ctx))
	var nilResolver *Resolver
	assert.Equal(t, Default, nilResolver.Hosts(ctx))
}

func TestResolver_WriteServesAtOnce(t *testing.T) {
	sec := &memSection{}
	r := NewResolver(sec, Config{})
	got, err := r.Write(context.Background(), []string{"CDN.example.com"}, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, []string{"https://cdn.example.com"}, got)
	assert.Equal(t, []string{"https://cdn.example.com"}, sec.value.Hosts, "stored normalized")
	reads := sec.reads
	assert.Equal(t, got, r.Hosts(context.Background()))
	assert.Equal(t, reads, sec.reads, "this replica serves its own write without reading it back")

	_, err = r.Write(context.Background(), []string{"*"}, "admin@example.com")
	require.ErrorIs(t, err, ErrInvalid)
}

func settingsMux(r *Resolver) *http.ServeMux {
	mux := http.NewServeMux()
	RegisterSettings(mux, func(h http.Handler) http.Handler { return h }, r,
		func(*http.Request) string { return "admin@example.com" })
	return mux
}

func call(t *testing.T, mux http.Handler, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, settingsPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestSettingsRoutes(t *testing.T) {
	sec := &memSection{}
	mux := settingsMux(NewResolver(sec, Config{}))

	rec := call(t, mux, http.MethodGet, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var got Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, Default, got.Hosts)
	assert.Equal(t, Default, got.Configured)

	rec = call(t, mux, http.MethodPut, `{"hosts":["cdn.example.com"]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []string{"https://cdn.example.com"}, got.Hosts)

	rec = call(t, mux, http.MethodPut, `{"hosts":null}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, string(mustField(t, rec.Body.Bytes(), "hosts")), "null turns embeds off and answers []")

	rec = call(t, mux, http.MethodPut, `{"hosts":["cdn.example.com; script-src *"]}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "cdn.example.com; script-src *", "the refusal names the entry")
	assert.Empty(t, sec.value.Hosts, "nothing stored")

	assert.Equal(t, http.StatusBadRequest, call(t, mux, http.MethodPut, `not json`).Code)

	sec.readErr = errors.New("down")
	assert.Equal(t, http.StatusInternalServerError, call(t, mux, http.MethodPut, `{"hosts":["a.example.com"]}`).Code)
}

func mustField(t *testing.T, body []byte, key string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &m))
	return m[key]
}
