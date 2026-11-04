package appdbwatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/appdbdrift"
	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

var now = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

type fakeSettings struct {
	stored *settingsjson.Stored[Settings]
	err    error
	setErr error
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
	if f.setErr != nil {
		return f.setErr
	}
	f.stored = &settingsjson.Stored[Settings]{Value: v, UpdatedBy: author, UpdatedAt: now}
	return nil
}

type sender struct{ docs []string }

func (s *sender) Send(_ context.Context, _ []string, key string, p notification.Payload) int {
	raw, _ := json.Marshal(p)
	s.docs = append(s.docs, key+" "+string(raw))
	return 1
}

type memAlerts struct{ rows map[string]levelalert.Record }

func (m *memAlerts) List(context.Context, string) ([]levelalert.Record, error) {
	out := make([]levelalert.Record, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *memAlerts) Upsert(_ context.Context, _ string, f levelalert.Finding, at time.Time) error {
	m.rows[f.Key] = levelalert.Record{Key: f.Key, Level: f.Level, Title: f.Title, Since: at}
	return nil
}

func (m *memAlerts) Delete(_ context.Context, key string) error {
	delete(m.rows, key)
	return nil
}

// fakeKit serves connections; a connection named "down" does not answer.
type fakeKit struct{ conns []string }

func (k *fakeKit) ListConnections() []toolkit.ConnectionDetail {
	out := make([]toolkit.ConnectionDetail, 0, len(k.conns))
	for _, c := range k.conns {
		out = append(out, toolkit.ConnectionDetail{Name: c})
	}
	return out
}

func (*fakeKit) Query(_ context.Context, s postgres.Statement) (*postgres.Result, error) {
	if s.Connection == "down" {
		return nil, errors.New("appdb: connection \"down\" could not reach its database")
	}
	if strings.Contains(s.SQL, "pg_backend_pid()") {
		return &postgres.Result{
			Columns: []postgres.Column{{Name: "state"}, {Name: "state_seconds"}, {Name: "query"}},
			Rows:    [][]any{{"idle in transaction", 900.0, "UPDATE secret_table SET x = 'canary-unit'"}},
		}, nil
	}
	return &postgres.Result{}, nil
}

func (*fakeKit) Describe(context.Context, string, string) (*postgres.Description, error) {
	return &postgres.Description{Tables: []postgres.Table{{Name: "orders", Columns: []postgres.TableColumn{{Name: "coupon"}}}}}, nil
}

func (*fakeKit) DescribeBuilt(context.Context, string, string, []postgres.Migration) (*postgres.Description, error) {
	return &postgres.Description{Tables: []postgres.Table{{Name: "orders"}}}, nil
}

func (*fakeKit) Status(context.Context, string, string, []postgres.Migration) (*postgres.LedgerStatus, error) {
	return &postgres.LedgerStatus{Applied: []postgres.Applied{{Version: 1}}}, nil
}

type store struct {
	migrations []postgres.Migration
	err        error
}

func (s store) List(context.Context, string, string) ([]postgres.Migration, error) {
	return s.migrations, s.err
}

func TestSettings(t *testing.T) {
	d := Defaults()
	require.NoError(t, d.Validate())
	assert.Equal(t, 5*time.Minute, d.Interval())
	n := Settings{}.normalized()
	assert.Equal(t, DefaultIntervalSeconds, n.IntervalSeconds)
	assert.Equal(t, DefaultHistoryDays, n.HistoryDays)
	assert.Equal(t, []string{}, n.Channels)

	for name, s := range map[string]Settings{
		"empty channel":  {Channels: []string{" "}},
		"interval":       {IntervalSeconds: 5},
		"history":        {HistoryDays: MaxHistoryDays + 1},
		"long running":   {LongRunningSeconds: -1},
		"negative":       {InvalidIndexes: levelalert.Thresholds{Warning: -1}},
		"critical below": {DriftObjects: levelalert.Thresholds{Warning: 5, Critical: 2}},
	} {
		require.Error(t, s.Validate(), name)
	}

	f := &fakeSettings{}
	v, err := Read(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, Defaults(), v.Settings)
	require.NoError(t, f.Set(context.Background(), Settings{Channels: []string{"ops"}}, "a@example.com"))
	v, err = Read(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, "a@example.com", v.UpdatedBy)
	assert.NotEmpty(t, v.UpdatedAt)
	_, err = Read(context.Background(), &fakeSettings{err: errors.New("down")})
	require.Error(t, err)
}

func TestFindings(t *testing.T) {
	s := Sample{
		TransactionIDAge: 1_200_000_000, Connections: 96, MaxConnections: 100, IdleInTransaction: 2,
		LongestIdleSeconds: 400, InvalidIndexes: 1, MaxDeadRatio: 0.6, MaxDeadTable: "public.orders",
		Reachable: true, DriftObjects: 7, DriftUnchecked: 1,
		Drift: []appdbdrift.Result{{Schema: "public", Checked: true, Differences: []appdbdrift.Difference{
			{Kind: "column", Object: "orders.a", Change: "added"},
			{Kind: "column", Object: "orders.b", Change: "added"},
			{Kind: "column", Object: "orders.c", Change: "added"},
			{Kind: "column", Object: "orders.d", Change: "added"},
			{Kind: "column", Object: "orders.e", Change: "added"},
			{Kind: "column", Object: "orders.f", Change: "added"},
			{Kind: "column", Object: "orders.g", Change: "added"},
		}}, {Schema: "app", Checked: false, Reason: "the ledger disagrees"}},
	}
	got := map[string]levelalert.Level{}
	var drift levelalert.Finding
	for _, f := range Findings("shop", s, Defaults(), "https://p/x") {
		got[strings.TrimPrefix(f.Key, "appdb:shop:")] = f.Level
		if strings.HasSuffix(f.Key, ConditionDrift+":public") {
			drift = f
		}
		assert.Equal(t, "https://p/x", f.Link)
	}
	assert.Equal(t, map[string]levelalert.Level{
		ConditionTransactionIDAge: levelalert.Critical, ConditionIdleInTransaction: levelalert.Warning,
		ConditionConnections: levelalert.Critical, ConditionInvalidIndexes: levelalert.Warning,
		ConditionDeadTuples: levelalert.Critical, ConditionDrift + ":public": levelalert.Warning,
	}, got, "an unchecked schema raises nothing and hides nothing")
	assert.Contains(t, drift.Body, "column public.orders.a added")
	assert.Contains(t, drift.Body, "and 2 more")
	assert.Empty(t, Findings("shop", Sample{
		Reachable: true, DriftUnchecked: 1,
		Drift: []appdbdrift.Result{{Schema: "app", Reason: "x"}},
	}, Defaults(), ""), "an unchecked schema is not drift")
	assert.Equal(t, "1", trim(1))
	assert.Equal(t, "0.50", trim(0.5))
}

// svcRig is a service over sqlmock, the sender and the alert store.
type svcRig struct {
	svc    *Service
	mock   sqlmock.Sqlmock
	snd    *sender
	alerts *memAlerts
}

func newSvc(t *testing.T, kit Kit, settings *fakeSettings, migrations appdbdrift.Migrations) svcRig {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	snd, alerts := &sender{}, &memAlerts{rows: map[string]levelalert.Record{}}
	svc := New(Deps{
		DB: db, Settings: settings, Migrations: migrations, Engine: levelalert.New(alerts, snd),
		Kit:     func() Kit { return kit },
		PageURL: func(c string) string { return "https://p/" + c },
		Now:     func() time.Time { return now },
	})
	require.NotNil(t, svc)
	return svcRig{svc: svc, mock: mock, snd: snd, alerts: alerts}
}

const recentQ = `SELECT EXISTS \(SELECT 1 FROM appdb_health_samples WHERE sampled_at > \$1\)`

func TestPass(t *testing.T) {
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Settings{
		Channels:                 []string{"ops"},
		IdleInTransactionSeconds: levelalert.Thresholds{Warning: 60}, DriftObjects: DefaultDriftObjects,
	}}}
	kit := &fakeKit{conns: []string{"shop", "down"}}
	r := newSvc(t, kit, settings, store{migrations: []postgres.Migration{{Version: 1, Schema: "public"}}})
	svc, mock, snd, alerts := r.svc, r.mock, r.snd, r.alerts
	mock.ExpectQuery(recentQ).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	recorded := make([]string, 0, len(alerts.rows))
	for range 2 {
		mock.ExpectExec(`INSERT INTO appdb_health_samples`).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`DELETE FROM appdb_health_samples WHERE sampled_at < \$1`).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, svc.Pass(context.Background()))
	require.NoError(t, mock.ExpectationsWereMet())
	for k := range alerts.rows {
		recorded = append(recorded, k)
	}
	assert.ElementsMatch(t, []string{"appdb:shop:idle_in_transaction", "appdb:shop:drift:public"}, recorded,
		"an unreachable connection is not judged")
	all := strings.Join(snd.docs, "\n")
	assert.Contains(t, all, "https://p/shop")
	assert.NotContains(t, all, "canary-unit", "an alert carries no statement text")
	assert.NotContains(t, all, "secret_table")

	mock.ExpectQuery(recentQ).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(true))
	require.ErrorIs(t, svc.Pass(context.Background()), bgloop.ErrSkipped)
}

func TestPass_Failures(t *testing.T) {
	ctx := context.Background()
	r := newSvc(t, &fakeKit{}, &fakeSettings{err: errors.New("down")}, nil)
	svc := r.svc
	require.Error(t, svc.Pass(ctx))

	r = newSvc(t, &fakeKit{}, &fakeSettings{}, nil)
	svc = r.svc
	mock := r.mock
	mock.ExpectQuery(recentQ).WillReturnError(errors.New("down"))
	require.Error(t, svc.Pass(ctx))

	r = newSvc(t, &fakeKit{conns: []string{"shop"}}, &fakeSettings{}, nil)
	svc, mock = r.svc, r.mock
	mock.ExpectQuery(recentQ).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectExec(`INSERT INTO appdb_health_samples`).WillReturnError(errors.New("full"))
	require.ErrorContains(t, svc.Pass(ctx), "recording")

	r = newSvc(t, &fakeKit{}, &fakeSettings{}, nil)
	svc, mock = r.svc, r.mock
	mock.ExpectQuery(recentQ).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	mock.ExpectExec(`DELETE FROM appdb_health_samples`).WillReturnError(errors.New("x"))
	require.ErrorContains(t, svc.Pass(ctx), "pruning")

	db, m2, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	noKit := New(Deps{DB: db, Settings: &fakeSettings{}, Kit: func() Kit { return nil }, Now: func() time.Time { return now }})
	m2.ExpectQuery(recentQ).WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(false))
	require.ErrorIs(t, noKit.Pass(ctx), bgloop.ErrSkipped)
	assert.Nil(t, New(Deps{}))
	var nilSvc *Service
	nilSvc.Start(ctx)
	nilSvc.Stop()
}

func TestDriftOf(t *testing.T) {
	ctx := context.Background()
	assert.Empty(t, DriftOf(ctx, &fakeKit{}, nil, "shop"))
	got := DriftOf(ctx, &fakeKit{}, store{err: errors.New("down")}, "shop")
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "reading the migrations")
	got = DriftOf(ctx, &fakeKit{}, store{migrations: []postgres.Migration{{Schema: "public", Version: 1}, {Schema: "public", Version: 2}, {Schema: "app", Version: 1}}}, "shop")
	assert.Len(t, got, 2, "one check per schema")
}

func TestHistoryLatestAndGauges(t *testing.T) {
	ctx := context.Background()
	r := newSvc(t, &fakeKit{}, &fakeSettings{}, nil)
	svc, mock := r.svc, r.mock
	raw, _ := json.Marshal(Sample{TransactionIDAge: 9, IdleInTransaction: 2, MaxDeadRatio: 0.3, Reachable: true, DriftObjects: 1})
	mock.ExpectQuery(`FROM appdb_health_samples\s+WHERE connection = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"connection", "sampled_at", "summary"}).AddRow("shop", now, raw))
	points, err := svc.History(ctx, "shop", 7)
	require.NoError(t, err)
	require.Len(t, points, 1)
	assert.Equal(t, int64(9), points[0].Sample.TransactionIDAge)

	mock.ExpectQuery(`SELECT DISTINCT ON \(connection\)`).
		WillReturnRows(sqlmock.NewRows([]string{"connection", "sampled_at", "summary"}).AddRow("shop", now, raw))
	g := svc.Gauges(ctx)
	require.Len(t, g, 1)
	assert.Equal(t, int64(2), g[0].IdleInTransaction)
	assert.InDelta(t, 0.3, g[0].DeadTupleRatio, 1e-9)
	assert.Equal(t, int64(1), g[0].DriftObjects)

	mock.ExpectQuery(`SELECT DISTINCT ON \(connection\)`).WillReturnError(errors.New("down"))
	assert.Empty(t, svc.Gauges(ctx))
	mock.ExpectQuery(`FROM appdb_health_samples\s+WHERE connection = \$1`).
		WillReturnRows(sqlmock.NewRows([]string{"connection", "sampled_at", "summary"}).AddRow("shop", now, []byte("{bad")))
	_, err = svc.History(ctx, "shop", 7)
	require.Error(t, err)
	mock.ExpectQuery(`FROM appdb_health_samples\s+WHERE connection = \$1`).WillReturnError(errors.New("down"))
	_, err = svc.History(ctx, "shop", 7)
	require.Error(t, err)

	bad := &Service{d: Deps{Settings: &fakeSettings{err: errors.New("x")}}}
	assert.Empty(t, bad.Gauges(ctx))
}

func TestSaveSettings(t *testing.T) {
	ctx := context.Background()
	f := &fakeSettings{}
	r := newSvc(t, &fakeKit{}, f, nil)
	svc := r.svc
	require.Error(t, svc.SaveSettings(ctx, Settings{IntervalSeconds: 1}, "a"))
	require.NoError(t, svc.SaveSettings(ctx, Settings{Channels: []string{"ops"}}, "a"))
	v, err := svc.Settings(ctx)
	require.NoError(t, err)
	assert.Equal(t, DefaultIntervalSeconds, v.IntervalSeconds, "a saved setting is filled where it left a value empty")
	f.setErr = errors.New("down")
	require.Error(t, svc.SaveSettings(ctx, Settings{}, "a"))
}

func TestStartStop(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	svc := New(Deps{DB: db, Settings: &fakeSettings{}, Kit: func() Kit { return nil }, Tick: time.Hour, Now: func() time.Time { return now }})
	svc.Start(context.Background())
	svc.Stop()
	svc.Stop()
}

func TestSmallPieces(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.NotNil(t, NewSettingsStore(db))
	svc := New(Deps{DB: db, Settings: &fakeSettings{}, Kit: func() Kit { return nil }})
	assert.Equal(t, DefaultTick, svc.d.Tick)
	assert.Empty(t, svc.pageURL("shop"), "no page without an address")
}
