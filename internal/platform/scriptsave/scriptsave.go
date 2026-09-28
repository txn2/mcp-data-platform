// Package scriptsave is the one gate a managed script's source crosses on
// every save (#1913), for the manage_script tool and the portal editor alike:
// the formatter and the lint (internal/platform/scriptlint), the script's
// tests and the statements they reach (#1939, #1940), and what the new
// version does differently from the saved one (#1942).
//
// A script created since tests were required is saved only with at least one
// test, every test passing, and at least MinCoverage percent of its statements
// reached; one created since #1952 also only while its tests read every output
// their executions produce (script.Script.OutputsReadOptional). A script saved before (script.Script.TestsOptional) saves without
// tests; a version of it that has tests must keep them passing and may not
// reach fewer statements than the version before it.
//
// A new version of a saved script replays the script's recent recorded runs
// through both versions and compares what they produced, and compares what
// each reaches. A difference is a change in what the automation does, and the
// save then needs a plain-language summary of it and the agent's confirmation
// that the person the automation runs for agreed. A person approves behavior,
// never code: there is no second approver.
package scriptsave

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptbehavior"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlint"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scripttest"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// MinCoverage is the share of its statements a script's tests must reach.
const MinCoverage = 80.0

// RecentRuns is how many of a script's latest recorded runs a save replays.
const RecentRuns = 5

// Gate holds what a save is checked against. The zero value checks the lint
// and the tests, with no recording a test names readable.
type Gate struct {
	// Recordings is where recordings are read from; nil reads none.
	Recordings scriptrec.Store
	// Destinations is the deployment's bucket destinations, which an export
	// a test or a replay reaches resolves against.
	Destinations []script.Destination
	// MaxMemoryBytes is the memory one test or replay may hold.
	MaxMemoryBytes int64
	// Contracts is what an answer a test declares is held to (#1953); nil
	// checks none.
	Contracts scripttest.Contracts
}

// Caller is who is saving.
type Caller struct {
	Email string
	Admin bool
}

// Request is one save.
type Request struct {
	// Existing is the script saved into, nil for a new one.
	Existing *script.Script
	// Name labels the script in tracebacks.
	Name   string
	Source string
	Caller Caller
	// ChangeSummary is what the new version does differently, in plain
	// language, and Agreed the confirmation that the person the automation
	// runs for agreed to it.
	ChangeSummary string
	Agreed        bool
}

// Result is what the gate made of a save.
type Result struct {
	// Lint is the formatter's and the lint's result; Lint.Source is what a
	// save stores.
	Lint scriptlint.Result `json:"-"`
	// Tests is the tests' report, nil when the save was refused before they
	// ran.
	Tests *scripttest.Report `json:"tests,omitempty"`
	// Differences is what the new version does differently. Replayed is the
	// recorded runs both versions replayed, and NotCompared the ones the
	// saved version itself could not replay.
	Differences []scriptbehavior.Difference `json:"differences"`
	Replayed    []string                    `json:"replayed"`
	NotCompared []string                    `json:"not_compared,omitempty"`
	// Refusal is why the save is refused, empty when it goes through.
	Refusal string `json:"refusal,omitempty"`
	// ChangeNeeded is true when the refusal is only that a behavior change
	// needs a summary and the person's agreement.
	ChangeNeeded bool `json:"change_needed,omitempty"`
	// ChangeSummary and ChangeAgreedBy are what the saved version carries,
	// empty when there is nothing.
	ChangeSummary  string `json:"-"`
	ChangeAgreedBy string `json:"-"`
}

// Apply sets the formatted source and the change the save carries on sc.
func (r Result) Apply(sc *script.Script) {
	sc.Source, sc.ChangeSummary, sc.ChangeAgreedBy = r.Lint.Source, r.ChangeSummary, r.ChangeAgreedBy
}

// Refused reports whether the save is refused.
func (r Result) Refused() bool { return r.Refusal != "" }

// Check puts one save through every gate, stopping at the first that refuses.
func (g *Gate) Check(ctx context.Context, req Request) Result {
	res := Result{Differences: []scriptbehavior.Difference{}, Replayed: []string{}}
	res.Lint = scriptlint.Check(req.Source, scriptlint.For(req.Existing))
	if len(res.Lint.Refused) > 0 {
		res.Refusal = scriptlint.Detail(res.Lint.Refused)
		return res
	}
	source := res.Lint.Source
	if res.Refusal = g.checkTests(ctx, req, source, &res); res.Refusal != "" {
		return res
	}
	g.compare(ctx, req, source, &res)
	if len(res.Differences) > 0 && (strings.TrimSpace(req.ChangeSummary) == "" || !req.Agreed) {
		res.ChangeNeeded = true
		res.Refusal = changeRefusal(res.Differences)
		return res
	}
	if s := strings.TrimSpace(req.ChangeSummary); s != "" && req.Agreed {
		res.ChangeSummary, res.ChangeAgreedBy = s, req.Caller.Email
	}
	return res
}

// checkTests runs the new source's tests and returns why they refuse the
// save, or "".
func (g *Gate) checkTests(ctx context.Context, req Request, source string, res *Result) string {
	report, err := scripttest.Run(ctx, g.testRequest(req, source))
	if err != nil {
		return "the tests could not be run, so the source was not saved: " + err.Error()
	}
	res.Tests = report
	required := req.Existing == nil || !req.Existing.TestsOptional
	switch {
	case len(report.Tests) == 0 && required:
		return "the source has no tests, so it was not saved: a script is saved with at least one " +
			"test_* function that replays a recorded run (testing.replay(\"<run id>\"), the id run_draft returns) " +
			"and asserts on what main() produced"
	case report.Failed > 0:
		return "a test failed, so the source was not saved: " + failures(report)
	case required && report.Coverage.Percent < MinCoverage:
		return fmt.Sprintf("the tests reach %d of %d statements (%.0f%%), under the %.0f%% a script is saved with, so it was not saved; "+
			"add tests that reach lines %s", report.Coverage.Covered, report.Coverage.Statements,
			report.Coverage.Percent, MinCoverage, lines(report.Coverage.MissedLines))
	// After coverage: an output behind a branch no test takes is the
	// coverage rule's to report (#1952).
	case len(report.Tests) > 0 && (req.Existing == nil || !req.Existing.OutputsReadOptional) && len(report.Unread) > 0:
		return "the tests leave what the script produced unread, so it was not saved: " + unread(report.Unread) +
			". Assert on each: a row's column (out.exports[0].rows[0][\"region\"]) or the whole rows " +
			"(assert.eq(out.exports[0].rows, [...])), out.state, out.notifies[i], out.result"
	case !required && len(report.Tests) > 0:
		return g.keepsCoverage(ctx, req, report)
	}
	return ""
}

// keepsCoverage refuses a version of a script saved before tests were
// required whose tests reach a smaller share of it than the version before's.
func (g *Gate) keepsCoverage(ctx context.Context, req Request, report *scripttest.Report) string {
	previous, err := scripttest.Run(ctx, g.testRequest(req, req.Existing.Source))
	if err != nil || len(previous.Tests) == 0 || previous.Coverage.Percent <= report.Coverage.Percent {
		return ""
	}
	return fmt.Sprintf("the tests reach %.0f%% of the script, where the saved version's reach %.0f%%, so it was not saved; "+
		"a version may not lower its coverage: add tests that reach lines %s",
		report.Coverage.Percent, previous.Coverage.Percent, lines(report.Coverage.MissedLines))
}

func (g *Gate) testRequest(req Request, source string) scripttest.Request {
	return scripttest.Request{
		Source: source, Name: req.Name, Destinations: g.Destinations,
		Load: g.Loader(req.Existing, req.Caller), MaxMemoryBytes: g.MaxMemoryBytes,
		Contracts: g.Contracts,
	}
}

// compare fills the differences between the saved version and source.
func (g *Gate) compare(ctx context.Context, req Request, source string, res *Result) {
	if req.Existing == nil || req.Existing.Source == source {
		return
	}
	res.Differences = append(res.Differences,
		scriptbehavior.Reach(scriptrun.Validate(req.Existing.Source), scriptrun.Validate(source))...)
	if g.Recordings == nil || req.Existing.ID == "" {
		return
	}
	recent, err := g.Recordings.Recent(ctx, req.Existing.ID, RecentRuns)
	if err != nil {
		res.Differences = append(res.Differences, scriptbehavior.Difference{
			Kind: scriptbehavior.KindFails, Detail: "the recorded runs could not be read to compare the versions: " + err.Error(),
		})
		return
	}
	for _, stored := range recent {
		rec, err := scriptrec.Decode(stored.Data)
		if err != nil {
			res.NotCompared = append(res.NotCompared, stored.RunID)
			continue
		}
		before := scripttest.Replay(ctx, g.testRequest(req, req.Existing.Source), rec)
		after := scripttest.Replay(ctx, g.testRequest(req, source), rec)
		diffs, compared := scriptbehavior.Compare(stored.RunID, before, after)
		if !compared {
			res.NotCompared = append(res.NotCompared, stored.RunID)
			continue
		}
		res.Replayed = append(res.Replayed, stored.RunID)
		res.Differences = append(res.Differences, diffs...)
	}
}

// Loader reads a recording a test names for caller: a recording is readable
// by an administrator, by the owner of the script it recorded, and a draft's
// by the person who ran it.
func (g *Gate) Loader(existing *script.Script, caller Caller) scripttest.Loader {
	return func(ctx context.Context, runID string) (*scriptrec.Recording, error) {
		if g.Recordings == nil {
			return nil, scriptrec.ErrNotFound
		}
		stored, err := g.Recordings.Get(ctx, runID)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store names the read
		}
		if !Readable(stored.Meta, existing, caller) {
			return nil, scriptrec.ErrNotFound
		}
		if !stored.Replayable() {
			return nil, fmt.Errorf("the run kept no recording: %s", stored.Reason)
		}
		return scriptrec.Decode(stored.Data)
	}
}

// Readable reports whether caller may read a recording, under the rules run
// history is read by: an administrator, and the current owner of existing
// when it recorded existing. A draft is also its author's, who ran it with
// their own access; a run is not its former owner's once the script moved.
func Readable(m scriptrec.Meta, existing *script.Script, caller Caller) bool {
	switch {
	case caller.Admin:
		return true
	case m.Kind == scriptrec.KindDraft && caller.Email != "" && strings.EqualFold(m.RecordedBy, caller.Email):
		return true
	default:
		return existing != nil && m.ScriptID != "" && m.ScriptID == existing.ID && existing.OwnedBy(caller.Email)
	}
}

// Keep marks the recordings the saved source's tests name, so the retention
// sweep keeps them while this is the script's latest version.
func (g *Gate) Keep(ctx context.Context, sc *script.Script, author string) error {
	if g.Recordings == nil || sc == nil || sc.ID == "" {
		return nil
	}
	return g.Recordings.Keep(ctx, sc.ID, author, scripttest.RecordingsNamed(sc.Source)) //nolint:wrapcheck // the store names the write
}

// changeRefusal is what a save that changes behavior without a summary and
// the person's agreement is told.
func changeRefusal(diffs []scriptbehavior.Difference) string {
	parts := make([]string, 0, len(diffs))
	for _, d := range diffs {
		parts = append(parts, d.String())
	}
	return "this version changes what the automation does, so it was not saved: " + strings.Join(parts, "; ") +
		". Tell the person it runs for what will now be different, in plain words, and once they agree, " +
		"save again with change_summary (what it will now do differently) and user_agreed=true."
}

// failures is every failed test, with its line.
func failures(report *scripttest.Report) string {
	var parts []string
	for _, t := range report.Tests {
		if t.Passed {
			continue
		}
		where := ""
		if t.Line > 0 {
			where = fmt.Sprintf(" (line %d)", t.Line)
		}
		parts = append(parts, t.Name+where+": "+t.Failure)
	}
	return strings.Join(parts, " | ")
}

// unread is each output no test read, as a sentence.
func unread(outputs []string) string {
	parts := make([]string, 0, len(outputs))
	for _, o := range outputs {
		parts = append(parts, o+" is never asserted on")
	}
	return strings.Join(parts, "; ")
}

func lines(ls []int) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		parts = append(parts, fmt.Sprint(l))
	}
	return strings.Join(parts, ", ")
}
