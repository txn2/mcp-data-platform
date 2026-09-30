package webhookapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

func TestStatusEveryListIsEmptyWithNoSources(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	svc := &fakeService{overview: whadmin.Overview{Now: now, Since: now.Add(-time.Hour), Step: time.Minute}}
	rec := do(newMux(svc), http.MethodGet, statusPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"generated_at": "2026-09-29T12:00:00Z",
		"range": "hour",
		"from": "2026-09-29T11:00:00Z",
		"bucket_seconds": 60,
		"silent_after_seconds": 86400,
		"sources": [],
		"volume": [],
		"rejections": []
	}`, rec.Body.String(), "every list is [] when there is nothing, never null")
	assert.Equal(t, time.Hour, svc.span)
	assert.Equal(t, time.Minute, svc.step)
}

func TestStatusReportsEverySource(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	seen := now.Add(-time.Minute)
	svc := &fakeService{overview: whadmin.Overview{
		Now: now, Since: now.Add(-24 * time.Hour), Step: 15 * time.Minute,
		Sources: []whadmin.SourceOverview{
			{
				Source: stored, Health: whadmin.HealthFailing,
				Summary: whstore.Summary{
					LastHour: map[string]int64{"accepted": 3}, LastDay: map[string]int64{"accepted": 9},
					LastSegmentAt: &seen, Pending: 2, Failing: 1, LastError: "segment k: not gzip",
				},
			},
			{Source: whsource.Source{Name: "quiet", Enabled: true, Auth: whsource.Auth{Mode: whsource.AuthBasic}}, Health: whadmin.HealthSilent},
		},
		Volume:     []whstore.VolumePoint{{Source: "esp", At: now.Add(-15 * time.Minute), Outcome: "accepted", Count: 9}},
		Rejections: []whstore.Rejection{{Source: "esp", At: now, Outcome: "unauthorized", Reason: "the signature does not match"}},
	}}
	rec := do(newMux(svc), http.MethodGet, statusPath+"?range=day", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "s3cret", "the overview never carries a secret")
	assert.Equal(t, 24*time.Hour, svc.span)
	assert.Equal(t, 15*time.Minute, svc.step)

	var got StatusOverview
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "day", got.Range)
	assert.Equal(t, 900, got.BucketSeconds)
	require.Len(t, got.Sources, 2)
	esp := got.Sources[0]
	assert.Equal(t, "esp", esp.Name)
	assert.Equal(t, "failing", esp.Health)
	assert.Equal(t, whsource.AuthHMAC, esp.AuthMode)
	assert.Equal(t, "webhook_esp", esp.Table)
	assert.Equal(t, 1, esp.Failing)
	assert.Equal(t, 2, esp.Pending)
	assert.Equal(t, "segment k: not gzip", esp.LastError)
	require.NotNil(t, esp.LastEventAt)
	assert.Equal(t, int64(3), esp.LastHour["accepted"])

	quiet := got.Sources[1]
	assert.Equal(t, "silent", quiet.Health)
	assert.Nil(t, quiet.LastEventAt)
	assert.Contains(t, rec.Body.String(), `"last_hour":{}`, "a source with no counts has an empty map, never null")

	assert.Equal(t, []VolumePoint{{At: now.Add(-15 * time.Minute), Source: "esp", Outcome: "accepted", Count: 9}}, got.Volume)
	assert.Equal(t, []SourceRejection{{Source: "esp", At: now, Outcome: "unauthorized", Reason: "the signature does not match"}}, got.Rejections)
}

func TestStatusRefusals(t *testing.T) {
	rec := do(newMux(&fakeService{}), http.MethodGet, statusPath+"?range=week", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "range must be hour or day")

	rec = do(newMux(&fakeService{err: errors.New("pq: down")}), http.MethodGet, statusPath, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "pq:", "an unexpected failure is not echoed")
}
