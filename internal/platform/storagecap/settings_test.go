package storagecap

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

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

func TestReadDefaultsAndStored(t *testing.T) {
	ctx := context.Background()
	v, err := Read(ctx, &fakeSettings{})
	require.NoError(t, err)
	assert.Equal(t, Defaults(), v.Settings)
	assert.Equal(t, 180, v.HistoryDays)
	assert.Equal(t, levelalert.Thresholds{Warning: 75, Critical: 90}, v.UsedPercent)
	assert.True(t, v.DaysLeft.Below)

	stored := &fakeSettings{stored: &settingsjson.Stored[Settings]{
		Value: Settings{DatabaseLimitBytes: 5, DaysLeft: levelalert.Thresholds{Warning: 10}}, UpdatedBy: "admin", UpdatedAt: now,
	}}
	v, err = Read(ctx, stored)
	require.NoError(t, err)
	assert.Equal(t, int64(5), v.DatabaseLimitBytes)
	assert.Equal(t, DefaultHistoryDays, v.HistoryDays, "an unset period is the default")
	assert.True(t, v.DaysLeft.Below, "days left is lower-is-worse however it was stored")
	assert.NotNil(t, v.Channels)
	assert.NotNil(t, v.Budgets)
	assert.NotNil(t, v.SeaweedFS.VolumeURLs)
	assert.Equal(t, "admin", v.UpdatedBy)
	assert.Equal(t, "2026-10-17T12:00:00Z", v.UpdatedAt)

	_, err = Read(ctx, &fakeSettings{err: errors.New("down")})
	require.Error(t, err)

	budgets, err := BudgetsFrom(stored)(ctx)
	require.NoError(t, err)
	assert.Empty(t, budgets)
	_, err = BudgetsFrom(&fakeSettings{err: errors.New("down")})(ctx)
	require.Error(t, err)
}

func TestInputApply(t *testing.T) {
	base := Defaults()
	limit := int64(10 << 30)
	days := 30
	in := Input{
		Channels:           &[]string{" ops ", "ops", "db"},
		DatabaseLimitBytes: &limit,
		Budgets:            &map[string]int64{" portal ": 1 << 40},
		SeaweedFS:          &SeaweedFS{MasterURL: "http://master:9333/", VolumeURLs: []string{"http://v2:8080", " ", "http://v1:8080/"}},
		HistoryDays:        &days,
		UsedPercent:        &levelalert.Thresholds{Warning: 60, Critical: 80},
		DaysLeft:           &levelalert.Thresholds{Warning: 14, Critical: 3},
	}
	got, err := in.Apply(base)
	require.NoError(t, err)
	assert.Equal(t, []string{"ops", "db"}, got.Channels)
	assert.Equal(t, limit, got.DatabaseLimitBytes)
	assert.Equal(t, map[string]int64{"portal": 1 << 40}, got.Budgets)
	assert.Equal(t, SeaweedFS{MasterURL: "http://master:9333", VolumeURLs: []string{"http://v1:8080", "http://v2:8080"}}, got.SeaweedFS)
	assert.Equal(t, 30, got.HistoryDays)
	assert.Equal(t, 60.0, got.UsedPercent.Warning)
	assert.True(t, got.DaysLeft.Below)

	kept, err := Input{}.Apply(got)
	require.NoError(t, err)
	assert.Equal(t, got, kept, "absent fields keep what is stored")

	zero := int64(0)
	off, err := Input{DatabaseLimitBytes: &zero, UsedPercent: &levelalert.Thresholds{}}.Apply(got)
	require.NoError(t, err)
	assert.Zero(t, off.DatabaseLimitBytes)
	assert.Equal(t, levelalert.Thresholds{}, off.UsedPercent, "zero turns a threshold off")
}

func TestInputApplyRefusals(t *testing.T) {
	neg := int64(-1)
	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = string(rune('a' + i))
	}
	zeroDays, longDays := 0, 4000
	for name, in := range map[string]Input{
		"too many channels":        {Channels: &tooMany},
		"negative limit":           {DatabaseLimitBytes: &neg},
		"budget names no bucket":   {Budgets: &map[string]int64{" ": 1}},
		"budget not positive":      {Budgets: &map[string]int64{"b": 0}},
		"master not http":          {SeaweedFS: &SeaweedFS{MasterURL: "ftp://m"}},
		"master no host":           {SeaweedFS: &SeaweedFS{MasterURL: "http://"}},
		"volume not http":          {SeaweedFS: &SeaweedFS{MasterURL: "http://m", VolumeURLs: []string{"v:8080"}}},
		"volumes without a master": {SeaweedFS: &SeaweedFS{VolumeURLs: []string{"http://v:8080"}}},
		"history zero":             {HistoryDays: &zeroDays},
		"history too long":         {HistoryDays: &longDays},
		"percent over 100":         {UsedPercent: &levelalert.Thresholds{Warning: 50, Critical: 120}},
		"percent inverted":         {UsedPercent: &levelalert.Thresholds{Warning: 90, Critical: 80}},
		"days negative":            {DaysLeft: &levelalert.Thresholds{Warning: -1}},
		"days inverted":            {DaysLeft: &levelalert.Thresholds{Warning: 3, Critical: 7}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := in.Apply(Defaults())
			require.Error(t, err)
		})
	}
}

// TestSettingsRoundTripThroughTheSection reads back through the
// platform_settings section what was written, the days-left direction
// restored.
func TestSettingsRoundTripThroughTheSection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := NewSettingsStore(db)
	mock.ExpectExec(`INSERT INTO platform_settings`).WithArgs("storage", sqlmock.AnyArg(), "admin").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, store.Set(context.Background(), Defaults(), "admin"))
	mock.ExpectQuery(`FROM platform_settings WHERE section = \$1`).WithArgs("storage").
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
			AddRow([]byte(`{"days_left":{"warning":30,"critical":7},"history_days":90}`), "admin", now))
	v, err := Read(context.Background(), store)
	require.NoError(t, err)
	assert.Equal(t, 90, v.HistoryDays)
	assert.True(t, v.DaysLeft.Below)
	assert.Equal(t, levelalert.Level("critical"), v.DaysLeft.Of(3))
}
