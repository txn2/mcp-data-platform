package whalert

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

type fakeSettings struct {
	stored *settingsjson.Stored[Settings]
	err    error
}

func (f *fakeSettings) Get(context.Context) (settingsjson.Stored[Settings], error) {
	if f.err != nil {
		return settingsjson.Stored[Settings]{}, f.err
	}
	if f.stored == nil {
		return settingsjson.Stored[Settings]{}, settingsjson.ErrNotFound
	}
	return *f.stored, nil
}

func (f *fakeSettings) Set(_ context.Context, v Settings, author string) error {
	f.stored = &settingsjson.Stored[Settings]{Value: v, UpdatedBy: author, UpdatedAt: now}
	return nil
}

type fakeReader struct {
	spools    []whstore.SpoolState
	per       map[string]*whstore.SourceReadings
	spoolErr  error
	readErr   error
	countFrom time.Time
	ackFrom   time.Time
}

func (f *fakeReader) SpoolStates(context.Context) ([]whstore.SpoolState, error) {
	return f.spools, f.spoolErr
}

func (f *fakeReader) AlertReadings(_ context.Context, countsSince, ackSince time.Time) (map[string]*whstore.SourceReadings, error) {
	f.countFrom, f.ackFrom = countsSince, ackSince
	return f.per, f.readErr
}

type fakeSources struct {
	list []whsource.Source
	err  error
}

func (f *fakeSources) List(context.Context) ([]whsource.Source, error) { return f.list, f.err }

type sender struct{ keys []string }

// Send records the key without its episode, the time the level began.
func (s *sender) Send(_ context.Context, _ []string, key string, _ notification.Payload) int {
	s.keys = append(s.keys, key[:strings.LastIndex(key, ":")])
	return 1
}

type memStore struct{ rows map[string]levelalert.Record }

func (m *memStore) List(context.Context, string) ([]levelalert.Record, error) {
	out := make([]levelalert.Record, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memStore) Upsert(_ context.Context, _ string, f levelalert.Finding, at time.Time) error {
	m.rows[f.Key] = levelalert.Record{Key: f.Key, Level: f.Level, Since: at}
	return nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	delete(m.rows, key)
	return nil
}

func newService(t *testing.T, settings *fakeSettings, reader *fakeReader, sources *fakeSources) (*Service, *sender) {
	t.Helper()
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := &sender{}
	svc := New(Deps{
		DB: db, Settings: settings, Reader: reader, Sources: sources, Grace: 2 * time.Minute,
		Engine: levelalert.New(&memStore{rows: map[string]levelalert.Record{}}, s), BaseURL: "https://p",
		Now: func() time.Time { return now },
	})
	require.NotNil(t, svc)
	return svc, s
}

// TestPassWalksSpoolAgeAndResolves is #2149's first acceptance as the
// service sees it: a spool whose object store is stopped walks from warning
// to critical, and resolves when the store returns, one post per step.
func TestPassWalksSpoolAgeAndResolves(t *testing.T) {
	ctx := context.Background()
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Defaults()}}
	settings.stored.Value.Channels = []string{"ops"}
	reader := &fakeReader{}
	svc, s := newService(t, settings, reader, &fakeSources{list: []whsource.Source{{Name: "esp", Enabled: true}}})
	at := func(age time.Duration) []whstore.SpoolState {
		return []whstore.SpoolState{{Replica: "a", Events: 3, Oldest: ago(age), MaxAge: time.Hour, MaxBytes: 1 << 30, UpdatedAt: now}}
	}

	reader.spools = at(20 * time.Minute)
	require.NoError(t, svc.Pass(ctx))
	reader.spools = at(40 * time.Minute)
	require.NoError(t, svc.Pass(ctx))
	reader.spools = []whstore.SpoolState{{Replica: "a", MaxAge: time.Hour, MaxBytes: 1 << 30, UpdatedAt: now}}
	require.NoError(t, svc.Pass(ctx))
	assert.Equal(t, []string{
		"webhook:spool_age:a:warning", "webhook:spool_age:a:critical", "webhook:spool_age:a:resolved",
	}, s.keys)
	assert.Equal(t, now.Add(-AckSpan), reader.ackFrom)
	assert.Equal(t, now.Add(-UnauthorizedSpan).Truncate(time.Minute), reader.countFrom)
}

func TestPassReadsTheLongestRefusalSpan(t *testing.T) {
	long := 30
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Defaults()}}
	settings.stored.Value.Sources = map[string]SourceRules{"esp": {RefusalMinutes: &long}}
	reader := &fakeReader{}
	svc, _ := newService(t, settings, reader, &fakeSources{})
	require.NoError(t, svc.Pass(context.Background()))
	assert.Equal(t, now.Add(-30*time.Minute).Truncate(time.Minute), reader.countFrom)
}

func TestPassFailures(t *testing.T) {
	boom := errors.New("down")
	for name, mk := range map[string]func() (*fakeSettings, *fakeReader, *fakeSources){
		"settings": func() (*fakeSettings, *fakeReader, *fakeSources) {
			return &fakeSettings{err: boom}, &fakeReader{}, &fakeSources{}
		},
		"sources": func() (*fakeSettings, *fakeReader, *fakeSources) {
			return &fakeSettings{}, &fakeReader{}, &fakeSources{err: boom}
		},
		"spools": func() (*fakeSettings, *fakeReader, *fakeSources) {
			return &fakeSettings{}, &fakeReader{spoolErr: boom}, &fakeSources{}
		},
		"readings": func() (*fakeSettings, *fakeReader, *fakeSources) {
			return &fakeSettings{}, &fakeReader{readErr: boom}, &fakeSources{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			settings, reader, sources := mk()
			svc, _ := newService(t, settings, reader, sources)
			require.Error(t, svc.Pass(context.Background()))
		})
	}
	t.Run("engine", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		svc := New(Deps{
			DB: db, Settings: &fakeSettings{}, Reader: &fakeReader{}, Sources: &fakeSources{},
			Engine: levelalert.New(&brokenStore{}, nil),
		})
		require.ErrorContains(t, svc.Pass(context.Background()), "routing webhook alerts")
	})
}

type brokenStore struct{}

func (brokenStore) List(context.Context, string) ([]levelalert.Record, error) {
	return nil, errors.New("down")
}

func (brokenStore) Upsert(context.Context, string, levelalert.Finding, time.Time) error { return nil }
func (brokenStore) Delete(context.Context, string) error                                { return nil }

func TestLifecycle(t *testing.T) {
	assert.Nil(t, New(Deps{}))
	var nilSvc *Service
	nilSvc.Start(context.Background())
	nilSvc.Stop()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)
	svc := New(Deps{DB: db, Settings: &fakeSettings{}, Reader: &fakeReader{}, Sources: &fakeSources{}})
	assert.Equal(t, DefaultInterval, svc.d.Interval)
	svc.Start(context.Background())
	svc.Stop()
	svc.Stop()
}
