package whalert

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

// TestDefaultsAreTheTicketsTable pins the defaults #2149 names.
func TestDefaultsAreTheTicketsTable(t *testing.T) {
	d := Defaults()
	assert.Equal(t, levelalert.Thresholds{Warning: 50, Critical: 80}, d.SpoolFillPercent)
	assert.Equal(t, levelalert.Thresholds{Warning: 25, Critical: 60}, d.SpoolAgePercent)
	assert.Equal(t, levelalert.Thresholds{Warning: 3, Critical: 10}, d.DrainFailures)
	assert.Equal(t, 5.0, d.DrainFailingMinutes)
	assert.Equal(t, 120.0, d.NotReportingSeconds)
	assert.Equal(t, 5, *d.Defaults.RefusalMinutes)
	assert.Equal(t, levelalert.Thresholds{Warning: 10}, *d.Defaults.UnauthorizedPercent)
	assert.Equal(t, levelalert.Thresholds{Warning: 1000, Critical: 5000}, *d.Defaults.AckP99MS)
	assert.Equal(t, 0, *d.Defaults.SilentAfterMinutes, "silence is per-source opt-in")
	assert.True(t, d.SpoolVolumeFreePercent.Below)
}

func TestRead(t *testing.T) {
	ctx := context.Background()
	v, err := Read(ctx, &fakeSettings{})
	require.NoError(t, err)
	assert.Equal(t, Defaults(), v.Settings)

	stored := &fakeSettings{stored: &settingsjson.Stored[Settings]{Value: Settings{}, UpdatedBy: "admin", UpdatedAt: now}}
	v, err = Read(ctx, stored)
	require.NoError(t, err)
	assert.NotNil(t, v.Channels)
	assert.NotNil(t, v.Sources)
	assert.NotNil(t, v.Defaults.AckP99MS, "an unset per-source default is the built-in one")
	assert.Equal(t, "2026-10-17T12:00:30Z", v.UpdatedAt)

	_, err = Read(ctx, &fakeSettings{err: errors.New("down")})
	require.Error(t, err)
}

func TestInputApply(t *testing.T) {
	ten, minutes, secs := 10, 2.0, 60.0
	in := Input{
		Channels:               &[]string{"ops", " ops "},
		SpoolFillPercent:       &levelalert.Thresholds{Warning: 40, Critical: 70},
		SpoolAgePercent:        &levelalert.Thresholds{Warning: 20},
		DrainFailures:          &levelalert.Thresholds{Warning: 2, Critical: 4},
		DrainFailingMinutes:    &minutes,
		NotReportingSeconds:    &secs,
		SpoolVolumeFreePercent: &levelalert.Thresholds{Warning: 30, Critical: 15},
		Defaults:               &SourceRules{RefusalMinutes: &ten},
		Sources:                &map[string]SourceRules{" esp ": {AckP99MS: &levelalert.Thresholds{Warning: 200, Critical: 900}}},
	}
	got, err := in.Apply(Defaults())
	require.NoError(t, err)
	assert.Equal(t, []string{"ops"}, got.Channels)
	assert.Equal(t, 40.0, got.SpoolFillPercent.Warning)
	assert.Equal(t, 2.0, got.DrainFailingMinutes)
	assert.Equal(t, 60.0, got.NotReportingSeconds)
	assert.True(t, got.SpoolVolumeFreePercent.Below)
	assert.Equal(t, 10, *got.Defaults.RefusalMinutes)
	assert.NotNil(t, got.Defaults.AckP99MS, "a default left out is the built-in one")
	rules := got.RulesFor("esp")
	assert.Equal(t, 200.0, rules.AckP99MS.Warning)
	assert.Equal(t, 10, *rules.RefusalMinutes, "a source inherits what it does not set")

	kept, err := Input{}.Apply(got)
	require.NoError(t, err)
	assert.Equal(t, got, kept)
}

func TestInputApplyRefusals(t *testing.T) {
	neg, big := -1, 61
	negF := -1.0
	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = string(rune('a' + i))
	}
	for name, in := range map[string]Input{
		"too many channels":      {Channels: &tooMany},
		"fill over 100":          {SpoolFillPercent: &levelalert.Thresholds{Warning: 50, Critical: 150}},
		"fill inverted":          {SpoolFillPercent: &levelalert.Thresholds{Warning: 90, Critical: 80}},
		"failures negative":      {DrainFailures: &levelalert.Thresholds{Warning: -1}},
		"volume inverted":        {SpoolVolumeFreePercent: &levelalert.Thresholds{Warning: 10, Critical: 25}},
		"failing negative":       {DrainFailingMinutes: &negF},
		"not reporting negative": {NotReportingSeconds: &negF},
		"refusal negative":       {Defaults: &SourceRules{RefusalMinutes: &neg}},
		"refusal over an hour":   {Defaults: &SourceRules{RefusalMinutes: &big}},
		"silent negative":        {Sources: &map[string]SourceRules{"esp": {SilentAfterMinutes: &neg}}},
		"unauthorized over 100":  {Defaults: &SourceRules{UnauthorizedPercent: &levelalert.Thresholds{Warning: 120}}},
		"ack inverted":           {Sources: &map[string]SourceRules{"esp": {AckP99MS: &levelalert.Thresholds{Warning: 5000, Critical: 1000}}}},
		"source named nothing":   {Sources: &map[string]SourceRules{" ": {}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := in.Apply(Defaults())
			require.Error(t, err)
		})
	}
}

func TestSettingsSection(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery(`FROM platform_settings WHERE section = \$1`).WithArgs("webhook_alert").
		WillReturnRows(sqlmock.NewRows([]string{"value", "updated_by", "updated_at"}).
			AddRow([]byte(`{"channels":["ops"],"spool_volume_free_percent":{"warning":25,"critical":10}}`), "admin", now))
	v, err := Read(context.Background(), NewSettingsStore(db))
	require.NoError(t, err)
	assert.Equal(t, []string{"ops"}, v.Channels)
	assert.Equal(t, levelalert.Critical, v.SpoolVolumeFreePercent.Of(5), "free space is lower-is-worse as stored")
}
