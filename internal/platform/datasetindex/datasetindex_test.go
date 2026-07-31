package datasetindex

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestKind(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SourceKind, NewSource(nil, nil, 1).Kind())
	assert.Equal(t, SourceKind, NewSink(nil, "m", time.Minute).Kind())
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()
	var unset Config
	assert.True(t, unset.IsEnabled(), "an unset index is on: discoverability is not opt-in")
	assert.Equal(t, DefaultSyncInterval, unset.ResolvedSyncInterval())
	assert.Equal(t, DefaultMaxEntries, unset.ResolvedMaxEntries())

	off := false
	assert.False(t, Config{Enabled: &off}.IsEnabled())

	tuned := Config{SyncInterval: 90 * time.Second, MaxEntries: 7}
	assert.Equal(t, 90*time.Second, tuned.ResolvedSyncInterval())
	assert.Equal(t, 7, tuned.ResolvedMaxEntries())

	// A negative value is unset, not an inverted schedule.
	neg := Config{SyncInterval: -time.Second, MaxEntries: -3}
	assert.Equal(t, DefaultSyncInterval, neg.ResolvedSyncInterval())
	assert.Equal(t, DefaultMaxEntries, neg.ResolvedMaxEntries())
}

func TestIndexText(t *testing.T) {
	t.Parallel()
	full := IndexText(Entry{
		URN:         "urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)",
		Name:        "sales.orders",
		Description: "Refunds are netted at close of month.",
		Tags:        []string{"finance", "curated"},
		Domain:      "Revenue",
	})
	assert.Equal(t, "sales.orders\nRefunds are netted at close of month.\nfinance curated\nRevenue", full)

	// A bare dataset does not pad the embedded text with blank lines.
	assert.Equal(t, "sales.orders", IndexText(Entry{Name: "sales.orders"}))
	assert.Empty(t, IndexText(Entry{}))
}

func TestSearcherGating(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	off := false
	cases := []struct {
		name string
		db   *sql.DB
		cfg  Config
	}{
		{name: "no database", db: nil, cfg: Config{}},
		{name: "operator opt-out", db: db, cfg: Config{Enabled: &off}},
	}
	for _, tc := range cases {
		got := Searcher(tc.db, tc.cfg)
		// Compared with == rather than assert.Nil: a typed nil (*Store)(nil)
		// boxed in the interface satisfies assert.Nil but is NOT nil to the
		// caller's `!= nil` check, and would nil-panic on first use.
		if got != nil {
			t.Errorf("%s: Searcher = %#v, want a true nil interface", tc.name, got)
		}
	}
	assert.NotNil(t, Searcher(db, Config{}))
}

// fakeRegistry records Register calls for RegisterConsumer tests.
type fakeRegistry struct {
	calls int
	err   error
}

func (f *fakeRegistry) Register(_ indexjobs.Source, _ indexjobs.Sink) error {
	f.calls++
	return f.err
}

func TestRegisterConsumer(t *testing.T) {
	t.Parallel()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	reg := &fakeRegistry{}
	require.NoError(t, RegisterConsumer(reg, db, &stubLister{}, "model-x", Config{}))
	assert.Equal(t, 1, reg.calls)

	failing := &fakeRegistry{err: errors.New("boom")}
	assert.Error(t, RegisterConsumer(failing, db, &stubLister{}, "model-x", Config{}))
}

// stubLister returns a fixed page set, recording the filters it was asked for.
type stubLister struct {
	pages   [][]semantic.TableSearchResult
	filters []semantic.SearchFilter
	err     error
	calls   int
}

func (s *stubLister) SearchTables(_ context.Context, f semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	s.filters = append(s.filters, f)
	if s.err != nil {
		return nil, s.err
	}
	if s.calls >= len(s.pages) {
		s.calls++
		return nil, nil
	}
	page := s.pages[s.calls]
	s.calls++
	return page, nil
}
