// Package scripttest runs a managed script's tests (#1939) and measures the
// statements they reach (#1940).
//
// A test is a top-level def named test_*, versioned with the script's source.
// It names at most one recording with testing.replay("<run id>"), as a string
// literal, calls main() or any other function, and asserts on what the
// execution produced with the assert module and testing.outputs(). Every host
// call is answered from the recording (internal/platform/scriptrec), so a test
// never reaches an upstream, and a write binding records what it would have
// written: an export is kept as its rows, a notify as its arguments. A call the
// recording holds no answer for fails the test, naming the call.
//
// Each test runs in a fresh module, so nothing one test does is seen by the
// next. Every test is instrumented for statement coverage; the statements of
// the tests themselves are not counted.
package scripttest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptdialect"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Loader reads the recording a test names, applying who may read it: a
// recording is its script owner's and an administrator's.
type Loader func(ctx context.Context, runID string) (*scriptrec.Recording, error)

// Request is one run of a source's tests.
type Request struct {
	Source string
	Name   string
	// Destinations is the deployment's bucket destinations, which an export a
	// test reaches resolves against as a run's would.
	Destinations []script.Destination
	Load         Loader
	// MaxMemoryBytes is the memory one test may hold; zero sets no budget.
	MaxMemoryBytes int64
	// Contracts is what a declared answer is held to; nil checks none.
	Contracts Contracts
	// Libraries is where the libraries the source loads are read from
	// (#1941), the same pinned source a run reads.
	Libraries scriptlib.Source
}

// Result is one test's outcome.
type Result struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Recording string `json:"recording,omitempty"`
	// Failure is what failed: the assertion, or the error the execution
	// stopped with. Line is the line of the script it happened at.
	Failure string `json:"failure,omitempty"`
	Line    int    `json:"line,omitempty"`
	Log     string `json:"log,omitempty"`
	// Notes is what the author is told about a test that passed or failed
	// alike: an answer declared for a tool that declares no answer contract,
	// which nothing checked.
	Notes []string `json:"notes,omitempty"`
}

// Coverage is the statements the tests reached.
type Coverage struct {
	Statements  int     `json:"statements"`
	Covered     int     `json:"covered"`
	Percent     float64 `json:"percent"`
	MissedLines []int   `json:"missed_lines"`
}

// Report is what running a source's tests found.
type Report struct {
	Tests    []Result `json:"tests"`
	Passed   int      `json:"passed"`
	Failed   int      `json:"failed"`
	Coverage Coverage `json:"coverage"`
	// Unread is every output the tests' executions produced that no test
	// read (#1952), as output "weekly" column "region".
	Unread []string `json:"unread"`
}

// OK reports whether the source has tests and every one passed.
func (r *Report) OK() bool { return len(r.Tests) > 0 && r.Failed == 0 }

// Run runs every test in the request's source, in source order. The error is
// the platform's or the source's (it does not parse, a test names its
// recording wrongly); a failing test is a Result.
func Run(ctx context.Context, req Request) (*Report, error) {
	if req.Name == "" {
		// The label a run gives the script, so a failure's line is read
		// off the frames that carry it.
		req.Name = "script"
	}
	file, err := scriptdialect.Options.Parse(req.Name, req.Source, 0)
	if err != nil {
		return nil, fmt.Errorf("parsing the script: %w", err)
	}
	replays, err := Replays(file)
	if err != nil {
		return nil, err
	}
	cover := scriptdialect.NewCoverage()
	read := newReads()
	seen := tally{cover: cover, reads: read}
	report := &Report{Tests: []Result{}}
	for _, name := range scriptdialect.Tests(file) {
		res := runOne(ctx, req, name, replays[name], seen)
		if res.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Tests = append(report.Tests, res)
	}
	missed := cover.MissedLines()
	if missed == nil {
		missed = []int{}
	}
	report.Coverage = Coverage{
		Statements: cover.Total(), Covered: cover.Covered(),
		Percent: cover.Percent(), MissedLines: missed,
	}
	report.Unread = read.unread()
	return report, nil
}

// tally is what every test of a source adds to: the statements reached and
// the outputs read.
type tally struct {
	cover *scriptdialect.Coverage
	reads *reads
}

// runOne runs one test.
func runOne(ctx context.Context, req Request, name, recording string, seen tally) Result {
	res := Result{Name: name, Recording: recording}
	// A test that names no recording has no answers: its tool calls fail
	// naming themselves, and its outputs are previewed, as a draft's are.
	rec := &scriptrec.Recording{Header: scriptrec.Header{Preview: true}}
	if recording != "" {
		loaded, err := req.Load(ctx, recording)
		if err != nil {
			res.Failure = fmt.Sprintf("reading recording %s: %v", recording, err)
			return res
		}
		rec = loaded
	}
	out, result, err := execute(ctx, req, execution{entry: name, recording: recording, rec: rec, cover: seen.cover, reads: seen.reads})
	out.register(seen.reads)
	if result != nil {
		res.Log = result.Log
	}
	for _, tool := range out.declared.unchecked {
		res.Notes = append(res.Notes, "the answer declared for "+tool+" was not checked against the tool: "+
			tool+" declares no answer contract")
	}
	switch {
	case err != nil:
		res.Failure, res.Line = describeFailure(err, req.Name)
	case out.asserts == 0:
		res.Failure = name + " made no assertion; a test asserts on what the script produced, " +
			"as assert.eq(testing.outputs().exports[0].row_count, 3)"
	case out.failures > 0:
		// A test that the script fails where it should: a failure that holds
		// whatever the rows were is what it checks, so the rows are not
		// altered under it.
		res.Passed = true
	case out.nothing():
		// Nothing came of the rows, so there is nothing an assertion could
		// read from them.
		res.Passed = true
	default:
		res.Failure = insensitive(ctx, req, name, rec, out.declared.rows)
		res.Passed = res.Failure == ""
	}
	return res
}

// execution is one run of a source against a recording: the test entry
// names, or main() when it is empty, answered from replay (an exact replay of
// rec when nil), with the statements it reaches recorded in cover when set.
type execution struct {
	entry, recording string
	rec              *scriptrec.Recording
	replay           *scriptrec.Replay
	cover            *scriptdialect.Coverage
	// alter, when set, alters the rows of every answer the test declares.
	alter func([]any) []any
	// reads, when set, records what the test reads of its outputs.
	reads *reads
}

// execute runs one execution with every output observed.
func execute(ctx context.Context, req Request, e execution) (*produced, *scriptrun.Result, error) {
	replay, rec := e.replay, e.rec
	if replay == nil {
		replay = scriptrec.NewReplay(rec)
	}
	entry, recording, cover := e.entry, e.recording, e.cover
	h := rec.Header
	live := scriptlive.New(scriptrun.MaxLogBytes, 0)
	answers := &declared{contracts: req.Contracts, alter: e.alter}
	replay.WithAnswers(answers)
	inputs := &scriptrun.TestInputs{}
	out := &produced{replay: replay, live: live, declared: answers, reads: e.reads}
	opts := scriptrun.Options{
		Source: req.Source, Name: req.Name,
		RunID: h.RunID, FireTime: h.FireTime, Params: h.Params, State: h.State, RunURL: h.RunURL,
		Caller: replay, Exporter: replay.Exporter(), Destinations: req.Destinations,
		Live: live, MaxSteps: scriptrun.RunMaxSteps, Timeout: scriptrun.DraftTimeout,
		MaxRows: h.MaxRows, MaxMemoryBytes: req.MaxMemoryBytes, Libraries: req.Libraries,
		Test: &scriptrun.TestHooks{
			Entry:   entry,
			Env:     starlark.StringDict{scriptrun.TestingName: testingModule(recording, out, inputs), scriptrun.AssertName: assertModule(&out.asserts, &out.failures)},
			Observe: out.observe,
			Cover:   cover,
			Inputs:  inputs,
		},
	}
	result, err := scriptrun.Run(ctx, opts)
	return out, result, err //nolint:wrapcheck // the run's own failure, which the caller describes
}

// testingModule is the testing module one test is handed.
func testingModule(recording string, out *produced, inputs *scriptrun.TestInputs) *starlarkstruct.Module {
	return &starlarkstruct.Module{Name: "testing", Members: starlark.StringDict{
		"replay": starlark.NewBuiltin("testing.replay", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			var id string
			if err := starlark.UnpackArgs(b.Name(), args, kwargs, "run_id", &id); err != nil {
				return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
			}
			if id != recording {
				return nil, fmt.Errorf("in %s: a test names its recording once, as a string literal", b.Name())
			}
			return starlark.None, nil
		}),
		"outputs": starlark.NewBuiltin("testing.outputs", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
			if err := starlark.UnpackArgs(b.Name(), args, kwargs); err != nil {
				return nil, err //nolint:wrapcheck // the interpreter's message names the builtin
			}
			return out.value()
		}),
		"answer":  starlark.NewBuiltin("testing.answer", out.declared.answerBuiltin),
		"set_run": setRunBuiltin(inputs),
	}}
}

// describeFailure is what a failed test reports: an assertion's own message,
// or the error the execution stopped with, and the line of the script it
// happened at.
func describeFailure(err error, name string) (msg string, line int) {
	var failure *AssertionError
	if errors.As(err, &failure) {
		return failure.Message, failure.Line
	}
	msg = err.Error()
	var missing *scriptrec.MissingError
	if errors.As(err, &missing) {
		msg = missing.Error()
	}
	var evalErr *starlark.EvalError
	if !errors.As(err, &evalErr) {
		return msg, 0
	}
	switch {
	case errors.Is(err, scriptrun.ErrStepLimit), errors.Is(err, scriptrun.ErrTimeout):
		// The limit's own sentence, without the backtrace it carries.
		msg, _, _ = strings.Cut(msg, ": Traceback")
	case missing == nil:
		msg = evalErr.Msg
	}
	for _, fr := range evalErr.CallStack {
		if fr.Pos.Filename() == name && fr.Pos.Line > 0 {
			line = int(fr.Pos.Line)
		}
	}
	return msg, line
}

// Replays is the recording each test names, by test. A test that names none
// is absent. A test that names its recording by anything but one string
// literal is refused: the recording a test replays is read without running
// it, to keep it past the retention sweep.
func Replays(file *syntax.File) (map[string]string, error) {
	out := map[string]string{}
	for _, s := range file.Stmts {
		def, ok := s.(*syntax.DefStmt)
		if !ok || !scriptdialect.IsTest(def) {
			continue
		}
		id, err := replayOf(def)
		if err != nil {
			return nil, err
		}
		if id != "" {
			out[def.Name.Name] = id
		}
	}
	return out, nil
}

// replayOf is the recording one test names, "" when it names none.
func replayOf(def *syntax.DefStmt) (string, error) {
	var id string
	var err error
	syntax.Walk(def, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok || err != nil || !isReplay(call.Fn) {
			return err == nil
		}
		lit, ok := literalArg(call)
		start, _ := call.Span()
		switch {
		case !ok:
			err = fmt.Errorf("line %d: %s names its recording as a string literal, as testing.replay(\"<run id>\")", start.Line, def.Name.Name)
		case id != "":
			err = fmt.Errorf("line %d: %s names a second recording; a test replays one", start.Line, def.Name.Name)
		default:
			id = lit
		}
		return err == nil
	})
	return id, err
}

// RecordingsNamed is every recording the source's tests name, sorted, or nil
// when the source does not parse.
func RecordingsNamed(source string) []string {
	file, err := scriptdialect.Options.Parse("script", source, 0)
	if err != nil {
		return nil
	}
	replays, err := Replays(file)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(replays))
	for _, id := range replays {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func isReplay(fn syntax.Expr) bool {
	dot, ok := fn.(*syntax.DotExpr)
	if !ok || dot.Name.Name != "replay" {
		return false
	}
	id, ok := dot.X.(*syntax.Ident)
	return ok && id.Name == scriptrun.TestingName
}

func literalArg(call *syntax.CallExpr) (string, bool) {
	if len(call.Args) != 1 {
		return "", false
	}
	lit, ok := call.Args[0].(*syntax.Literal)
	if !ok || lit.Token != syntax.STRING {
		return "", false
	}
	s, ok := lit.Value.(string)
	return s, ok && s != ""
}
