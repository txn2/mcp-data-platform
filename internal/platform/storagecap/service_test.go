package storagecap

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

type recordingSender struct{ keys []string }

// Send records the key without its episode, the time the level began.
func (r *recordingSender) Send(_ context.Context, _ []string, key string, _ notification.Payload) int {
	r.keys = append(r.keys, key[:strings.LastIndex(key, ":")])
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

// expectReadings queues the reads one report makes, in order.
func expectReadings(mock sqlmock.Sqlmock, dbBytes int64) {
	mock.ExpectQuery(`SELECT pg_database_size\(current_database\(\)\)`).
		WillReturnRows(sqlmock.NewRows([]string{"size"}).AddRow(dbBytes))
	mock.ExpectQuery(`FROM pg_class c JOIN pg_namespace`).
		WillReturnRows(sqlmock.NewRows([]string{"relname", "index", "bytes", "rows"}).
			AddRow("audit_logs", false, int64(600), int64(10)).AddRow("idx_x", true, int64(50), int64(0)))
	mock.ExpectQuery(`FROM storage_usage ORDER BY bucket, purpose`).
		WillReturnRows(sqlmock.NewRows([]string{"bucket", "purpose", "bytes", "objects", "listed_at"}).
			AddRow("portal", "portal_assets", int64(70), int64(7), now).
			AddRow("portal", "thumbnails", int64(10), int64(1), nil))
	mock.ExpectQuery(`FROM storage_reconciles ORDER BY purpose`).
		WillReturnRows(sqlmock.NewRows([]string{"purpose", "orphaned", "dangling", "reconciled_at"}).
			AddRow("portal_assets", int64(2), int64(0), now))
	mock.ExpectQuery(`FROM storage_scans WHERE id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"d", "o", "f"}).AddRow(1.5, int64(8), now))
	mock.ExpectQuery(`FROM storage_samples\s+WHERE sampled_at >= \$1 ORDER BY store, sampled_at`).
		WillReturnRows(sqlmock.NewRows([]string{"store", "at", "used", "total"}).
			AddRow("database", now.Add(-10*day), int64(500), int64(1000)).
			AddRow("database", now.Add(-5*day), int64(700), nil))
}

// svcRig is a Service over sqlmock with the sender and alert store it routes
// through.
type svcRig struct {
	svc    *Service
	mock   sqlmock.Sqlmock
	sender *recordingSender
	alerts *memAlerts
}

func newSvc(t *testing.T, settings *fakeSettings, spools func(context.Context) ([]whstore.SpoolState, error)) svcRig {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sender, alerts := &recordingSender{}, &memAlerts{rows: map[string]levelalert.Record{}}
	svc := New(Deps{
		DB: db, Settings: settings, Spools: spools, EnvBudgets: map[string]int64{"portal": 1 << 40, "env-only": 50},
		Engine: levelalert.New(alerts, sender), PageURL: "https://p/portal/admin/storage",
		Now: func() time.Time { return now },
	})
	require.NotNil(t, svc)
	return svcRig{svc: svc, mock: mock, sender: sender, alerts: alerts}
}

func TestPassSamplesAndAlerts(t *testing.T) {
	ctx := context.Background()
	v := volumeServer(t, 1000, 960, 40)
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Settings{
		Channels: []string{"ops"}, DatabaseLimitBytes: 1000, Budgets: map[string]int64{"portal": 100},
		SeaweedFS:   SeaweedFS{MasterURL: "http://unused", VolumeURLs: []string{v.URL}},
		UsedPercent: DefaultUsedPercent, DaysLeft: DefaultDaysLeft,
	}}}
	fsTotal, fsFree := int64(1000), int64(50)
	spools := func(context.Context) ([]whstore.SpoolState, error) {
		return []whstore.SpoolState{
			{Replica: "a", Dir: "/spool", Bytes: 5, MaxBytes: 100, FsTotal: &fsTotal, FsFree: &fsFree, UpdatedAt: now.Add(-time.Minute)},
			{Replica: "gone", Dir: "/spool", UpdatedAt: now.Add(-time.Hour)},
		}, nil
	}
	rig := newSvc(t, settings, spools)
	svc, mock, sender, alerts := rig.svc, rig.mock, rig.sender, rig.alerts
	expectReadings(mock, 950)
	for range 5 {
		mock.ExpectExec(`INSERT INTO storage_samples`).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`DELETE FROM storage_samples WHERE sampled_at < \$1`).
		WithArgs(now.Add(-DefaultHistoryDays * day)).WillReturnResult(sqlmock.NewResult(0, 3))
	require.NoError(t, svc.Pass(ctx))
	require.NoError(t, mock.ExpectationsWereMet())

	assert.ElementsMatch(t, []string{
		"storage:database:used:critical", "storage:database:exhaustion:critical",
		"storage:bucket:portal:used:warning", "storage:seaweedfs:used:critical", "storage:spool:a:used:critical",
	}, sender.keys)
	assert.Len(t, alerts.rows, 5)

	// The next pass finds the database back under its thresholds: resolved.
	sender.keys = nil
	settings.stored.Value.DatabaseLimitBytes = 100000
	expectReadings(mock, 950)
	for range 5 {
		mock.ExpectExec(`INSERT INTO storage_samples`).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectExec(`DELETE FROM storage_samples`).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, svc.Pass(ctx))
	assert.Contains(t, sender.keys, "storage:database:used:resolved")
	assert.Contains(t, sender.keys, "storage:database:exhaustion:resolved")
}

func TestReportShape(t *testing.T) {
	ctx := context.Background()
	rig := newSvc(t, &fakeSettings{}, nil)
	svc, mock := rig.svc, rig.mock
	expectReadings(mock, 950)
	r, err := svc.Report(ctx)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, now, r.GeneratedAt)
	assert.Equal(t, int64(950), r.Database.Bytes)
	assert.Nil(t, r.Database.LimitBytes, "no limit set is unknown, not zero")
	assert.Len(t, r.Database.Tables, 2)
	assert.True(t, r.Database.Tables[1].Index)
	require.Len(t, r.Buckets, 2)
	assert.Nil(t, r.Buckets[1].ListedAt)
	require.NotNil(t, r.LastScan)
	assert.Equal(t, int64(8), r.LastScan.Objects)
	assert.Nil(t, r.SeaweedFS, "no cluster is named")
	assert.NotNil(t, r.Spools)

	stores := map[string]StoreView{}
	for _, s := range r.Stores {
		stores[s.Store] = s
	}
	require.Contains(t, stores, "database")
	assert.Nil(t, stores["database"].PercentUsed)
	require.NotNil(t, stores["database"].Rate7d, "the history and the live reading fit a rate")
	assert.Equal(t, int64(80), stores["bucket:portal"].UsedBytes, "a bucket's usage is the sum of its purposes")
	require.NotNil(t, stores["bucket:portal"].TotalBytes)
	assert.Equal(t, int64(1<<40), *stores["bucket:portal"].TotalBytes, "the environment's budget applies with none set")
	assert.Equal(t, int64(0), stores["bucket:env-only"].UsedBytes, "a budgeted bucket with nothing in it is listed")
}

func TestReportClusterFailureIsShownNotFatal(t *testing.T) {
	ctx := context.Background()
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Settings{
		SeaweedFS: SeaweedFS{MasterURL: "http://127.0.0.1:1"},
	}}}
	rig := newSvc(t, settings, nil)
	svc, mock := rig.svc, rig.mock
	svc.d.HTTP = &http.Client{Timeout: time.Second}
	expectReadings(mock, 1)
	r, err := svc.Report(ctx)
	require.NoError(t, err)
	require.NotNil(t, r.SeaweedFS)
	assert.NotEmpty(t, r.SeaweedFS.Error)
	for _, s := range r.Stores {
		assert.NotEqual(t, KindSeaweedFS, s.Kind)
	}
}

func TestReportFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	steps := []string{"size", "tables", "usage", "reconcile", "scan", "spools", "history"}
	for i, step := range steps {
		t.Run(step, func(t *testing.T) {
			spools := func(context.Context) ([]whstore.SpoolState, error) {
				if step == "spools" {
					return nil, boom
				}
				return nil, nil
			}
			rig := newSvc(t, &fakeSettings{}, spools)
			svc, mock := rig.svc, rig.mock
			queries := []string{
				`pg_database_size`, `FROM pg_class`, `FROM storage_usage`, `FROM storage_reconciles`, `FROM storage_scans`, "", `FROM storage_samples`,
			}
			for j := range i {
				if queries[j] == "" {
					continue
				}
				expectStep(mock, j)
			}
			if queries[i] != "" {
				mock.ExpectQuery(queries[i]).WillReturnError(boom)
			}
			_, err := svc.Report(ctx)
			require.Error(t, err)
		})
	}
	_, err := (func() (Report, error) {
		svc := newSvc(t, &fakeSettings{err: boom}, nil).svc
		return svc.Report(ctx)
	})()
	require.Error(t, err)
}

// expectStep queues one successful read of expectReadings by its position.
func expectStep(mock sqlmock.Sqlmock, j int) {
	switch j {
	case 0:
		mock.ExpectQuery(`pg_database_size`).WillReturnRows(sqlmock.NewRows([]string{"s"}).AddRow(int64(1)))
	case 1:
		mock.ExpectQuery(`FROM pg_class`).WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d"}))
	case 2:
		mock.ExpectQuery(`FROM storage_usage`).WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d", "e"}))
	case 3:
		mock.ExpectQuery(`FROM storage_reconciles`).WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c", "d"}))
	case 4:
		mock.ExpectQuery(`FROM storage_scans`).WillReturnRows(sqlmock.NewRows([]string{"a", "b", "c"}))
	}
}

func TestPassFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	t.Run("settings", func(t *testing.T) {
		svc := newSvc(t, &fakeSettings{err: boom}, nil).svc
		require.Error(t, svc.Pass(ctx))
	})
	t.Run("record", func(t *testing.T) {
		rig := newSvc(t, &fakeSettings{}, nil)
		svc, mock := rig.svc, rig.mock
		expectReadings(mock, 1)
		mock.ExpectExec(`INSERT INTO storage_samples`).WillReturnError(boom)
		require.ErrorContains(t, svc.Pass(ctx), "recording a storage sample")
	})
	t.Run("prune", func(t *testing.T) {
		rig := newSvc(t, &fakeSettings{}, nil)
		svc, mock := rig.svc, rig.mock
		expectReadings(mock, 1)
		for range 3 {
			mock.ExpectExec(`INSERT INTO storage_samples`).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		mock.ExpectExec(`DELETE FROM storage_samples`).WillReturnError(boom)
		require.ErrorContains(t, svc.Pass(ctx), "pruning")
	})
	t.Run("report", func(t *testing.T) {
		rig := newSvc(t, &fakeSettings{}, nil)
		svc, mock := rig.svc, rig.mock
		mock.ExpectQuery(`pg_database_size`).WillReturnError(boom)
		require.Error(t, svc.Pass(ctx))
	})
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	rig := newSvc(t, &fakeSettings{}, nil)
	svc, mock := rig.svc, rig.mock
	mock.ExpectQuery(`FROM storage_samples\s+WHERE store = \$1 AND sampled_at >= \$2`).
		WithArgs("database", now.Add(-30*day)).
		WillReturnRows(sqlmock.NewRows([]string{"at", "used", "total"}).AddRow(now, int64(5), int64(10)).AddRow(now, int64(6), nil))
	got, err := svc.History(ctx, "database", 30)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, int64(10), *got[0].Total)
	assert.Nil(t, got[1].Total)

	mock.ExpectQuery(`FROM storage_samples`).WillReturnRows(sqlmock.NewRows([]string{"at", "used", "total"}))
	empty, err := svc.History(ctx, "database", 1)
	require.NoError(t, err)
	assert.NotNil(t, empty)

	mock.ExpectQuery(`FROM storage_samples`).WillReturnError(errors.New("down"))
	_, err = svc.History(ctx, "database", 1)
	require.Error(t, err)
	mock.ExpectQuery(`FROM storage_samples`).WillReturnRows(sqlmock.NewRows([]string{"at"}).AddRow(now))
	_, err = svc.History(ctx, "database", 1)
	require.Error(t, err)
}

func TestNewAndLifecycle(t *testing.T) {
	assert.Nil(t, New(Deps{}))
	var nilSvc *Service
	nilSvc.Start(context.Background())
	nilSvc.Stop()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery(`SELECT pg_try_advisory_lock`).WillReturnRows(sqlmock.NewRows([]string{"ok"}).AddRow(false))
	svc := New(Deps{DB: db, Settings: &fakeSettings{}, Interval: time.Hour})
	assert.Equal(t, time.Hour, svc.d.Interval)
	assert.NotNil(t, svc.d.HTTP)
	svc.Start(context.Background())
	svc.Stop()
	svc.Stop()
	assert.Equal(t, DefaultInterval, New(Deps{DB: db, Settings: &fakeSettings{}}).d.Interval)
}

// TestABucketOnTheClusterGrowsIntoItsFreeSpace: a bucket on SeaweedFS with no
// budget reads the cluster's free space as its own; a budgeted bucket keeps
// its budget.
func TestABucketOnTheClusterGrowsIntoItsFreeSpace(t *testing.T) {
	v := volumeServer(t, 1000, 600, 400)
	settings := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Settings{
		Budgets:   map[string]int64{"env-only": 50},
		SeaweedFS: SeaweedFS{MasterURL: "http://unused", VolumeURLs: []string{v.URL}},
	}}}
	rig := newSvc(t, settings, nil)
	svc, mock := rig.svc, rig.mock
	svc.d.OnSeaweedFS = func(_ context.Context, bucket string) bool { return bucket == "portal" }
	svc.d.EnvBudgets = nil
	expectReadings(mock, 1)
	r, err := svc.Report(context.Background())
	require.NoError(t, err)
	stores := map[string]StoreView{}
	for _, s := range r.Stores {
		stores[s.Store] = s
	}
	portal := stores["bucket:portal"]
	require.NotNil(t, portal.TotalBytes)
	assert.Equal(t, int64(80+400), *portal.TotalBytes, "what it holds plus the cluster's free space")
	assert.Equal(t, int64(400), *portal.FreeBytes)
	assert.Equal(t, KindSeaweedFS, portal.CapacityFrom)
	assert.Equal(t, "budget", stores["bucket:env-only"].CapacityFrom)
}
