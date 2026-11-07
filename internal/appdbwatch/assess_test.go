package appdbwatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/appdbhealth"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
)

func report() *appdbhealth.Report {
	return &appdbhealth.Report{
		Server:      appdbhealth.Server{TransactionIDAge: 227},
		Connections: appdbhealth.ConnectionUse{Max: 200, Total: 2},
		Tables: []appdbhealth.TableStats{
			{Schema: "public", Name: "orders", LiveTuples: 9000, DeadTuples: 1000, DeadRatio: 0.1},
			{Schema: "public", Name: "tiny", LiveTuples: 4, DeadTuples: 6, DeadRatio: 0.6},
		},
		Indexes: appdbhealth.IndexReport{
			Unused:    []appdbhealth.IndexRef{{Schema: "public", Table: "orders", Name: "orders_created_at_idx", SizeBytes: 2202009}},
			Duplicate: []appdbhealth.DuplicateIndexes{},
			Invalid:   []appdbhealth.IndexRef{},
		},
		Sessions: []appdbhealth.Session{},
		Blocking: []appdbhealth.Blocked{},
	}
}

// TestAssess_HealthyWithAdvice: nothing past a threshold is healthy, and the
// unused index is advice that does not change the status.
func TestAssess_HealthyWithAdvice(t *testing.T) {
	r := report()
	Assess(r, Defaults())
	assert.Equal(t, appdbhealth.StatusHealthy, r.Status)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, appdbhealth.ProblemUnusedIndex, r.Findings[0].Kind)
	assert.Equal(t, appdbhealth.LevelInfo, r.Findings[0].Level)
	assert.Equal(t, "orders_created_at_idx has not been used", r.Findings[0].Title)
	assert.Contains(t, r.Findings[0].Detail, "2.1 MiB on public.orders")
}

// TestAssess_IdleSessionIsAWarning is the acceptance sentence: a session idle
// in a transaction past the threshold is a warning finding naming its process
// and the threshold, and sets the status to warning.
func TestAssess_IdleSessionIsAWarning(t *testing.T) {
	r := report()
	r.Sessions = []appdbhealth.Session{
		{PID: 48213, State: "idle in transaction", StateSeconds: 14 * 60},
		{PID: 7, State: "idle in transaction", StateSeconds: 10},
		{PID: 9, State: "active", Seconds: 200},
	}
	r.Blocking = []appdbhealth.Blocked{{PID: 9, BlockedBy: []int64{48213}, Seconds: 40}}
	Assess(r, Defaults())
	assert.Equal(t, appdbhealth.StatusWarning, r.Status)
	first := r.Findings[0]
	assert.Equal(t, appdbhealth.ProblemIdleInTransaction, first.Kind)
	assert.Equal(t, string(levelalert.Warning), first.Level)
	assert.Equal(t, int64(48213), first.PID)
	assert.Equal(t, "A session has been idle in a transaction for 14 minutes", first.Title)
	assert.Contains(t, first.Detail, "The warning alert is set at 5 minutes.")
	kinds := make([]string, 0, len(r.Findings))
	for _, p := range r.Findings {
		kinds = append(kinds, p.Kind)
	}
	assert.Equal(t, []string{
		appdbhealth.ProblemIdleInTransaction, appdbhealth.ProblemBlocking,
		appdbhealth.ProblemLongRunning, appdbhealth.ProblemUnusedIndex,
	}, kinds, "the session under its threshold is not a finding; the most severe come first")
}

// TestAssess_AgreesWithTheAlerts holds the card's status to the level the
// alert engine posts for the same numbers, for every condition both judge.
func TestAssess_AgreesWithTheAlerts(t *testing.T) {
	cases := map[string]func(r *appdbhealth.Report){
		"none":        func(*appdbhealth.Report) {},
		"xid warning": func(r *appdbhealth.Report) { r.Server.TransactionIDAge = 600_000_000 },
		"xid critical": func(r *appdbhealth.Report) {
			r.Server.TransactionIDAge = 1_200_000_000
		},
		"connections": func(r *appdbhealth.Report) { r.Connections.Total = 196 },
		"idle critical": func(r *appdbhealth.Report) {
			r.Sessions = []appdbhealth.Session{{PID: 1, State: "idle in transaction (aborted)", StateSeconds: 4000}}
		},
		"invalid index": func(r *appdbhealth.Report) {
			r.Indexes.Invalid = []appdbhealth.IndexRef{{Schema: "public", Table: "items", Name: "items_bad"}}
		},
		"dead rows": func(r *appdbhealth.Report) { r.Tables[0].DeadRatio = 0.55 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := report()
			mutate(r)
			Assess(r, Defaults())
			worst := appdbhealth.StatusHealthy
			for _, f := range Findings("shop", Sample{Summary: appdbhealth.Summarize(r)}, Defaults(), "") {
				if f.Level == levelalert.Critical || worst == appdbhealth.StatusHealthy {
					worst = string(f.Level)
				}
			}
			assert.Equal(t, worst, r.Status)
		})
	}
}

// TestAssess_OffThresholdsJudgeNothing: a threshold of 0 is off.
func TestAssess_OffThresholdsJudgeNothing(t *testing.T) {
	r := report()
	r.Server.TransactionIDAge = 1_900_000_000
	r.Connections = appdbhealth.ConnectionUse{}
	r.Indexes.Duplicate = []appdbhealth.DuplicateIndexes{{Schema: "public", Table: "items", Names: []string{"a", "b"}}}
	Assess(r, Settings{})
	assert.Equal(t, appdbhealth.StatusHealthy, r.Status)
	require.Len(t, r.Findings, 2)
	assert.Equal(t, "2 indexes on items index the same columns", r.Findings[1].Title)
}

func TestWords(t *testing.T) {
	assert.Equal(t, "1 second", Duration(1))
	assert.Equal(t, "40 seconds", Duration(40))
	assert.Equal(t, "1 minute", Duration(60))
	assert.Equal(t, "3 hours", Duration(3*3600+5))
	assert.Equal(t, "2 days", Duration(2*86400))
	assert.Equal(t, "512 B", Bytes(512))
	assert.Equal(t, "2 KiB", Bytes(2048))
	assert.Equal(t, "1.5 GiB", Bytes(1536*1024*1024))
	assert.Equal(t, "150,000,000", grouped(150_000_000))
	assert.Equal(t, "-1,000", grouped(-1000))
	assert.Equal(t, "227", grouped(227))
}
