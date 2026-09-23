package outputshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeRuns answers with fixed runs and records the filter it was asked.
type fakeRuns struct {
	runs []script.Run
	err  error
	got  script.RunFilter
}

func (f *fakeRuns) ListRuns(_ context.Context, filter script.RunFilter) ([]script.Run, error) {
	f.got = filter
	return f.runs, f.err
}

var expiry = time.Unix(1_800_000_000, 0)

// asker is who calls, and whether a link minter is wired.
type asker struct {
	requester         string
	isAdmin, withURLs bool
}

func serve(t *testing.T, runs *fakeRuns, a asker, path string) (*httptest.ResponseRecorder, listResponse) {
	t.Helper()
	deps := Deps{
		Runs: runs,
		Caller: func(w http.ResponseWriter, _ *http.Request) (string, bool, bool) {
			if a.requester == "-" {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return "", false, false
			}
			return a.requester, a.isAdmin, true
		},
	}
	if a.withURLs {
		deps.ContentURL = func(assetID string, version int) (string, time.Time) {
			return "/signed/" + assetID + "/" + strconv.Itoa(version), expiry
		}
	}
	mux := http.NewServeMux()
	New(deps).Register(mux, func(h http.Handler) http.Handler { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody))
	var body listResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	}
	return rec, body
}

func sampleRuns() *fakeRuns {
	return &fakeRuns{runs: []script.Run{{
		ID: "run_2", ScriptID: "s1", Version: 3, Params: map[string]any{"tenant": "acme"},
		Outputs: []script.RunOutput{
			{Name: "sales", AssetID: "a1", AssetVersion: 2, Tags: []string{"report:sales"}, Metadata: map[string]any{"region": "west"}},
			{Name: "delivered", Destination: "acme-drop", Key: "x.csv"},
		},
	}, {
		ID: "run_1", ScriptID: "s2",
		Outputs: []script.RunOutput{{Name: "other", AssetID: "a2", AssetVersion: 1, Metadata: map[string]any{"region": "east"}}},
	}}}
}

func TestList_TheCallersRunsWithLinks(t *testing.T) {
	runs := sampleRuns()
	rec, body := serve(t, runs, asker{"apikey:reporting-app", false, true}, "/api/v1/portal/scripts/runs/outputs?script_id=s1")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "apikey:reporting-app", runs.got.RequestedBy)
	assert.Equal(t, "s1", runs.got.ScriptID)
	assert.Equal(t, script.RunStatusSucceeded, runs.got.Status)
	require.Equal(t, 2, body.Total, "portal outputs only: the delivered one has no asset")
	assert.Equal(t, "run_2", body.Data[0].RunID)
	assert.Equal(t, "acme", body.Data[0].Params["tenant"])
	assert.Equal(t, "/signed/a1/2", body.Data[0].ContentURL)
	assert.True(t, expiry.Equal(*body.Data[0].ContentURLExpiresAt), "the link expires when the minter said")
}

func TestList_FiltersByTagAndMetadata(t *testing.T) {
	_, body := serve(t, sampleRuns(), asker{"u", false, false}, "/api/v1/portal/scripts/runs/outputs?tag=report:sales&metadata.region=west")
	require.Equal(t, 1, body.Total)
	assert.Equal(t, "a1", body.Data[0].Output.AssetID)
	assert.Empty(t, body.Data[0].ContentURL, "no minter, no link")

	_, body = serve(t, sampleRuns(), asker{"u", false, false}, "/api/v1/portal/scripts/runs/outputs?metadata.region=north")
	assert.Equal(t, 0, body.Total)
	_, body = serve(t, sampleRuns(), asker{"u", false, false}, "/api/v1/portal/scripts/runs/outputs?tag=report:hr&tag=")
	assert.Equal(t, 0, body.Total)
}

func TestList_AnAdministratorReadsEveryRun(t *testing.T) {
	runs := sampleRuns()
	serve(t, runs, asker{"admin@example.com", true, false}, "/api/v1/portal/scripts/runs/outputs")
	assert.Empty(t, runs.got.RequestedBy)
}

func TestList_Refusals(t *testing.T) {
	rec, _ := serve(t, sampleRuns(), asker{"-", false, false}, "/api/v1/portal/scripts/runs/outputs")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	runs := sampleRuns()
	rec, body := serve(t, runs, asker{"", false, false}, "/api/v1/portal/scripts/runs/outputs")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`, "an unnamed caller asked for nothing")
	assert.Equal(t, 0, body.Total)

	rec, _ = serve(t, &fakeRuns{err: errors.New("boom")}, asker{"u", false, false}, "/api/v1/portal/scripts/runs/outputs")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "boom")
}
