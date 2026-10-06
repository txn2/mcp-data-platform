package flowhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type fakeAudit struct {
	events []audit.Event
	err    error
	asked  audit.QueryFilter
}

func (f *fakeAudit) Query(_ context.Context, filter audit.QueryFilter) ([]audit.Event, error) {
	f.asked = filter
	return f.events, f.err
}

const runSrc = "rows = platform.query(\"SELECT 1\", connection=\"warehouse\")\nplatform.export(\"daily\", rows[\"rows\"], format=\"csv\")\n"

func runDeps(run *script.Run, a AuditQuerier, versionErr error) Deps {
	return Deps{
		Load:     version(runSrc),
		SignedIn: signedIn(true),
		Version: func(_ context.Context, id string, n int) (*script.Version, error) {
			if versionErr != nil {
				return nil, versionErr
			}
			if n != 1 {
				return nil, nil //nolint:nilnil // the store contract: nil, nil is not found
			}
			return &script.Version{ScriptID: id, Version: 1, Source: runSrc}, nil
		},
		Run:    func(http.ResponseWriter, *http.Request) (*script.Run, bool) { return run, true },
		Audit:  a,
		ActsOn: func(*http.Request, string) bool { return true },
	}
}

func TestRunFlow_DrawsTheRunsAuditedCalls(t *testing.T) {
	created := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	run := &script.Run{
		ID: "dpx_1", ScriptID: "s1", Version: 1, Status: script.RunStatusFailed, CreatedAt: created,
		Cause: "upstream", Error: "Traceback (most recent call last):\n  script:2:16: in <toplevel>\nError in export: refused",
	}
	fa := &fakeAudit{events: []audit.Event{
		{
			ToolName: "trino_query", CallSite: []string{"1:22"}, DurationMS: 30, Success: true,
			Parameters: map[string]any{"sql": "SELECT 1", "connection": "warehouse"},
		},
		{ToolName: "s3_list", DurationMS: 2, Success: true},
	}}
	rec := get(t, runDeps(run, fa, nil), "/api/v1/portal/scripts/s1/runs/dpx_1/flow")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "dpx_1", fa.asked.SessionID)
	assert.Equal(t, "script", fa.asked.Source)
	assert.Equal(t, created.Add(-time.Minute), *fa.asked.StartTime)
	assert.Equal(t, maxRunCalls+1, fa.asked.Limit)

	var body struct {
		Status     string `json:"status"`
		Cause      string `json:"cause"`
		Calls      int    `json:"calls"`
		FailedNode string `json:"failed_node"`
		Nodes      map[string]struct {
			Calls  int  `json:"calls"`
			Failed bool `json:"failed"`
		} `json:"nodes"`
		Other     []map[string]any `json:"other_calls"`
		Truncated bool             `json:"calls_truncated"`
		Timeline  []struct {
			Tool      string `json:"tool"`
			Node      string `json:"node"`
			Arguments string `json:"arguments"`
		} `json:"timeline"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Timeline, 2)
	assert.Equal(t, "op:1", body.Timeline[0].Node)
	assert.JSONEq(t, `{"sql":"SELECT 1","connection":"warehouse"}`, body.Timeline[0].Arguments)
	assert.Empty(t, body.Timeline[1].Arguments, "a call audited with no arguments carries none")
	assert.Equal(t, "upstream", body.Cause)
	assert.Equal(t, 2, body.Calls)
	assert.Equal(t, 1, body.Nodes["op:1"].Calls)
	assert.Equal(t, "op:2", body.FailedNode)
	assert.True(t, body.Nodes["op:2"].Failed)
	require.Len(t, body.Other, 1)
	assert.False(t, body.Truncated)
}

// TestRunFlow_ArgumentsAreTheOwners proves whoever requested a run, who may
// read its flow, is not shown the arguments its calls were sent with the
// owner's roles: only the owner and administrators are.
func TestRunFlow_ArgumentsAreTheOwners(t *testing.T) {
	run := &script.Run{ID: "dpx_1", ScriptID: "s1", Version: 1, Status: script.RunStatusSucceeded}
	fa := &fakeAudit{events: []audit.Event{{
		ToolName: "trino_query", CallSite: []string{"1:22"}, Success: true,
		Parameters: map[string]any{"sql": "SELECT secret_column"},
	}}}
	for name, acts := range map[string]func(*http.Request, string) bool{
		"a requester": func(*http.Request, string) bool { return false },
		"no rule":     nil,
	} {
		t.Run(name, func(t *testing.T) {
			deps := runDeps(run, fa, nil)
			deps.ActsOn = acts
			rec := get(t, deps, "/api/v1/portal/scripts/s1/runs/dpx_1/flow")
			require.Equal(t, http.StatusOK, rec.Code)
			assert.NotContains(t, rec.Body.String(), "secret_column")
			assert.Contains(t, rec.Body.String(), "trino_query", "the call itself is still drawn")
		})
	}
}

func TestArgumentsText_CutsOnACharacterBoundary(t *testing.T) {
	short, cut := argumentsText(map[string]any{"a": 1})
	assert.JSONEq(t, `{"a":1}`, short)
	assert.False(t, cut)

	none, cut := argumentsText(nil)
	assert.Empty(t, none)
	assert.False(t, cut)

	// "é" is two bytes, so a cut at the bound lands inside one unless it
	// steps back to the character's start.
	long := strings.Repeat("é", maxArgumentBytes)
	text, cut := argumentsText(map[string]any{"q": long})
	assert.True(t, cut)
	assert.LessOrEqual(t, len(text), maxArgumentBytes)
	assert.True(t, utf8.ValidString(text), "the cut leaves whole characters")
	assert.True(t, strings.HasPrefix(text, `{"q":"é`))

	_, cut = argumentsText(map[string]any{"bad": make(chan int)})
	assert.False(t, cut, "arguments that do not marshal are left out")
}

func TestRunFlow_CapsTheCallsItReads(t *testing.T) {
	events := make([]audit.Event, maxRunCalls+1)
	run := &script.Run{ID: "dpx_1", ScriptID: "s1", Version: 1, Status: script.RunStatusSucceeded}
	rec := get(t, runDeps(run, &fakeAudit{events: events}, nil), "/api/v1/portal/scripts/s1/runs/dpx_1/flow")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Calls     int  `json:"calls"`
		Truncated bool `json:"calls_truncated"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, maxRunCalls, body.Calls)
	assert.True(t, body.Truncated)
}

func TestRunFlow_Refusals(t *testing.T) {
	run := &script.Run{ID: "dpx_1", ScriptID: "s1", Version: 1}
	path := "/api/v1/portal/scripts/s1/runs/dpx_1/flow"
	assert.Equal(t, http.StatusInternalServerError, get(t, runDeps(run, &fakeAudit{err: errors.New("boom")}, nil), path).Code)
	assert.Equal(t, http.StatusInternalServerError, get(t, runDeps(run, nil, errors.New("boom")), path).Code)
	run.Version = 4
	assert.Equal(t, http.StatusNotFound, get(t, runDeps(run, nil, nil), path).Code)

	d := runDeps(run, nil, nil)
	d.SignedIn = func(*http.Request) bool { return false }
	assert.Equal(t, http.StatusUnauthorized, get(t, d, path).Code)
	d.Run = func(w http.ResponseWriter, _ *http.Request) (*script.Run, bool) {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	d.SignedIn = signedIn(true)
	assert.Equal(t, http.StatusNotFound, get(t, d, path).Code)

	d.Run = nil
	assert.Equal(t, http.StatusNotFound, get(t, d, path).Code, "no run reader, no route")
}
