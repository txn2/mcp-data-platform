package appdbwatch

import (
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/appdbhealth"
	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
)

// Assess judges a report against the health settings (#2528): each condition
// an alert is posted for, at the level its thresholds give, one problem per
// session, index or table past it, then the advice no alert is posted for.
// The status is the most severe alert level, or healthy: it is the level the
// alert engine posts for the same numbers, so the card and the alerts agree.
// It writes Status and Findings into r.
func Assess(r *appdbhealth.Report, s Settings) {
	out := []appdbhealth.Problem{}
	out = append(out, idleProblems(r, s.IdleInTransactionSeconds)...)
	if p, ok := judged(s.TransactionIDAge, float64(r.Server.TransactionIDAge)); ok {
		p.Kind = appdbhealth.ProblemTransactionIDAge
		p.Title = fmt.Sprintf("The oldest transaction id is %s transactions old", grouped(r.Server.TransactionIDAge))
		p.Detail = "PostgreSQL refuses writes as the age nears 2^31; a vacuum held back by a long transaction lets it climb. " +
			alertAt(p, grouped(int64(p.Threshold)))
		out = append(out, p)
	}
	if share := appdbhealth.Summarize(r).ConnectionShare() * percent; share > 0 {
		if p, ok := judged(s.ConnectionsPercent, share); ok {
			p.Kind = appdbhealth.ProblemConnections
			p.Title = fmt.Sprintf("%d of %d connections are in use", r.Connections.Total, r.Connections.Max)
			p.Detail = "A new connection is refused once max_connections is reached. " + alertAt(p, trim(p.Threshold)+"%")
			out = append(out, p)
		}
	}
	out = append(out, invalidProblems(r, s.InvalidIndexes)...)
	out = append(out, deadProblems(r, s.DeadTuplePercent)...)
	out = append(out, advice(r)...)
	slices.SortStableFunc(out, func(a, b appdbhealth.Problem) int { return rank(a.Level) - rank(b.Level) })
	r.Findings = out
	r.Status = appdbhealth.StatusHealthy
	if len(out) > 0 && out[0].Level == string(levelalert.Critical) {
		r.Status = appdbhealth.StatusCritical
	} else if len(out) > 0 && out[0].Level == string(levelalert.Warning) {
		r.Status = appdbhealth.StatusWarning
	}
}

// judged is a value at the level t gives it, with the threshold it crossed;
// false below every threshold.
func judged(t levelalert.Thresholds, v float64) (appdbhealth.Problem, bool) {
	lvl := t.Of(v)
	switch lvl {
	case levelalert.Critical:
		return appdbhealth.Problem{Level: string(lvl), Value: v, Threshold: t.Critical}, true
	case levelalert.Warning:
		return appdbhealth.Problem{Level: string(lvl), Value: v, Threshold: t.Warning}, true
	}
	return appdbhealth.Problem{}, false
}

// alertAt is the sentence naming the threshold a problem crossed.
func alertAt(p appdbhealth.Problem, threshold string) string {
	return fmt.Sprintf("The %s alert is set at %s.", p.Level, threshold)
}

func idleProblems(r *appdbhealth.Report, t levelalert.Thresholds) []appdbhealth.Problem {
	out := []appdbhealth.Problem{}
	for _, x := range r.Sessions {
		if !strings.HasPrefix(x.State, "idle in transaction") {
			continue
		}
		p, ok := judged(t, x.StateSeconds)
		if !ok {
			continue
		}
		p.Kind, p.PID = appdbhealth.ProblemIdleInTransaction, x.PID
		p.Title = "A session has been idle in a transaction for " + Duration(x.StateSeconds)
		p.Detail = "Its open transaction holds the locks it took and keeps vacuum from reclaiming rows. " +
			alertAt(p, Duration(p.Threshold))
		out = append(out, p)
	}
	return out
}

func invalidProblems(r *appdbhealth.Report, t levelalert.Thresholds) []appdbhealth.Problem {
	out := []appdbhealth.Problem{}
	p, ok := judged(t, float64(len(r.Indexes.Invalid)))
	if !ok {
		return out
	}
	for _, i := range r.Indexes.Invalid {
		q := p
		q.Kind, q.Table, q.Index = appdbhealth.ProblemInvalidIndex, qualified(i.Schema, i.Table), i.Name
		q.Title = i.Name + " is invalid"
		q.Detail = "A failed CREATE INDEX CONCURRENTLY or REINDEX left an index PostgreSQL maintains on every write to " +
			i.Table + " and never reads. Reindex it, or drop it in a migration."
		out = append(out, q)
	}
	return out
}

func deadProblems(r *appdbhealth.Report, t levelalert.Thresholds) []appdbhealth.Problem {
	out := []appdbhealth.Problem{}
	for _, tb := range r.Tables {
		if tb.LiveTuples+tb.DeadTuples < appdbhealth.MinTuplesForRatio {
			continue
		}
		p, ok := judged(t, tb.DeadRatio*percent)
		if !ok {
			continue
		}
		p.Kind, p.Table = appdbhealth.ProblemDeadTuples, qualified(tb.Schema, tb.Name)
		p.Title = fmt.Sprintf("%.0f%% of %s's rows are dead", tb.DeadRatio*percent, tb.Name)
		p.Detail = "Rows are updated or deleted faster than autovacuum reclaims them, so the table and its indexes grow. " +
			alertAt(p, trim(p.Threshold)+"%")
		out = append(out, p)
	}
	return out
}

// advice is what an operator may act on that no alert is posted for: unused
// and duplicate indexes, blocked sessions and long-running statements.
func advice(r *appdbhealth.Report) []appdbhealth.Problem {
	out := []appdbhealth.Problem{}
	info := func(kind, title, detail string) appdbhealth.Problem {
		return appdbhealth.Problem{Kind: kind, Level: appdbhealth.LevelInfo, Title: title, Detail: detail}
	}
	for _, x := range r.Blocking {
		p := info(appdbhealth.ProblemBlocking, fmt.Sprintf("Process %d has waited on a lock for %s", x.PID, Duration(x.Seconds)),
			fmt.Sprintf("It is blocked by process %s.", joinPIDs(x.BlockedBy)))
		p.PID, p.Value = x.PID, x.Seconds
		out = append(out, p)
	}
	for _, x := range r.Sessions {
		if strings.HasPrefix(x.State, "idle in transaction") {
			continue
		}
		p := info(appdbhealth.ProblemLongRunning, "A statement has been running for "+Duration(x.Seconds),
			"A long statement holds its locks and its snapshot until it ends.")
		p.PID, p.Value = x.PID, x.Seconds
		out = append(out, p)
	}
	for _, i := range r.Indexes.Unused {
		p := info(appdbhealth.ProblemUnusedIndex, i.Name+" has not been used",
			fmt.Sprintf("%s on %s, 0 scans since the statistics were reset. Every write to %s still updates it.",
				Bytes(i.SizeBytes), qualified(i.Schema, i.Table), i.Table))
		p.Table, p.Index = qualified(i.Schema, i.Table), i.Name
		out = append(out, p)
	}
	for _, d := range r.Indexes.Duplicate {
		p := info(appdbhealth.ProblemDuplicateIndex, fmt.Sprintf("%d indexes on %s index the same columns", len(d.Names), d.Table),
			strings.Join(d.Names, ", ")+" index the same columns the same way; keep one and drop the rest in a migration.")
		p.Table, p.Index = qualified(d.Schema, d.Table), strings.Join(d.Names, ", ")
		out = append(out, p)
	}
	return out
}

// qualified names a table in its schema.
func qualified(schema, name string) string { return schema + "." + name }

// rank orders levels most severe first.
func rank(level string) int {
	switch level {
	case string(levelalert.Critical):
		return 0
	case string(levelalert.Warning):
		return 1
	}
	return 2
}

// Duration writes seconds the way a person says them: "40 seconds", "14
// minutes", "3 hours".
func Duration(secs float64) string {
	const minute, hour, day = 60, 3600, 86400
	unit := func(n int64, word string) string {
		if n == 1 {
			return "1 " + word
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	switch {
	case secs < minute:
		return unit(int64(secs), "second")
	case secs < hour:
		return unit(int64(secs/minute), "minute")
	case secs < day:
		return unit(int64(secs/hour), "hour")
	}
	return unit(int64(secs/day), "day")
}

// Bytes writes a size in binary units.
func Bytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	f, units := float64(n)/k, []string{"KiB", "MiB", "GiB", "TiB"}
	i := 0
	for f >= k && i < len(units)-1 {
		f /= k
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", f), ".0") + " " + units[i]
}

// thousands is how many digits a separator groups.
const thousands = 3

// grouped writes an integer with thousands separators.
func grouped(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%thousands == 0 {
			_ = b.WriteByte(',')
		}
		_, _ = b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func joinPIDs(pids []int64) string {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(parts, ", ")
}
