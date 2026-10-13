package scriptbgdraft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// reportEvery is how often a running draft's log and progress are written.
const reportEvery = 2 * time.Second

// grace is how long past its deadline a draft still reads as running: the
// time its replica takes to write how it ended. A draft its replica has not
// written for as long reads as stopped too, since a running draft is written
// every reportEvery.
const grace = time.Minute

// MaxBackground is how many background drafts one replica runs at once. A
// draft is an administrator's, but each holds a run's memory for up to an
// hour, so the next one waits for a slot rather than piling on.
const MaxBackground = 4

// ErrBusy is a background draft refused because the replica runs
// MaxBackground already.
var ErrBusy = fmt.Errorf("%d background drafts are already running on this replica; "+
	"read one with get_run and start the next when it ends", MaxBackground)

// Execution runs one draft with long's id, reporter and time limit, and
// returns what run_draft would have answered inline and whether the script
// failed. err is the platform's own failure.
type Execution func(ctx context.Context, long scriptdraft.Long) (draft map[string]any, failed bool, err error)

// Launcher starts background drafts and reads them back.
type Launcher struct {
	store  *Store
	cfg    Config
	every  time.Duration
	active atomic.Int32
}

// NewLauncher returns the launcher over store under cfg, or nil without a
// store: a deployment with nowhere to keep a background draft runs every
// draft inline, held to the default limit.
func NewLauncher(store *Store, cfg Config) *Launcher {
	if store == nil {
		return nil
	}
	return &Launcher{store: store, cfg: cfg, every: reportEvery}
}

// config is the launcher's scripts.draft section, the default without one.
func (l *Launcher) config() Config {
	if l == nil {
		return Config{}
	}
	return l.cfg
}

// Plan is Decide under the launcher's configuration.
func (l *Launcher) Plan(seconds int, admin bool) (limit time.Duration, background bool, err error) {
	return l.config().Decide(seconds, admin)
}

// Max is the longest a draft may run here.
func (l *Launcher) Max() time.Duration { return l.config().Max() }

// Start records a draft and runs it in the background for at most limit,
// returning its run id at once.
func (l *Launcher) Start(ctx context.Context, owner, script string, limit time.Duration, exec Execution) (string, error) {
	if l == nil {
		return "", errors.New("this deployment has nowhere to keep a background draft")
	}
	if l.active.Add(1) > MaxBackground {
		l.active.Add(-1)
		return "", ErrBusy
	}
	id, err := pkgsession.GenerateScriptSessionID()
	if err != nil {
		l.active.Add(-1)
		return "", fmt.Errorf("minting a run id: %w", err)
	}
	run := Run{ID: id, Owner: owner, Script: script, Deadline: time.Now().Add(limit)}
	if err := l.store.start(ctx, run); err != nil {
		l.active.Add(-1)
		return "", err
	}
	go l.execute(context.WithoutCancel(ctx), run, limit, exec)
	return id, nil
}

// execute runs the draft, reporting as it goes, and records how it ended.
func (l *Launcher) execute(ctx context.Context, run Run, limit time.Duration, exec Execution) {
	defer l.active.Add(-1)
	live := scriptlive.New(scriptrun.MaxLogBytes, scriptlive.DefaultMaxResultBytes)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		bgloop.Run(ctx, bgloop.Loop{
			Name: bgloop.NameScriptProgress, Every: l.every, Stop: quit,
			Body: func(ctx context.Context) error { return l.report(ctx, run.ID, live) },
		})
	}()
	draft, failed, err := exec(ctx, scriptdraft.Long{RunID: run.ID, Live: live, Timeout: limit})
	close(quit)
	<-done

	run.Status = StatusSucceeded
	if failed || err != nil {
		run.Status = StatusFailed
	}
	if err != nil {
		run.Error = err.Error()
	}
	run.Log, _ = live.Log()
	run.Progress = progressJSON(live)
	if draft != nil {
		run.Draft, _ = json.Marshal(draft)
	}
	if err := l.store.finish(ctx, run); err != nil {
		slog.ErrorContext(ctx, "scripts: recording a background draft's end failed", "run_id", run.ID, "error", err)
	}
}

// report writes one snapshot of the draft's log and progress.
func (l *Launcher) report(ctx context.Context, id string, live *scriptlive.Live) error {
	text, _ := live.Log()
	return l.store.report(ctx, id, text, progressJSON(live))
}

func progressJSON(live *scriptlive.Live) json.RawMessage {
	p := live.Progress()
	if p == nil {
		return nil
	}
	b, _ := json.Marshal(p)
	return b
}

// Read answers get_run for a background draft: ok is false when id is not
// one, so get_run reads its runs as before. A draft is its caller's and an
// administrator's, as an inline draft's answer is the caller's alone.
func (l *Launcher) Read(ctx context.Context, id, caller string, admin bool) (out *Run, ok bool, err error) {
	if l == nil {
		return nil, false, nil
	}
	run, err := l.store.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if run.Owner != caller && !admin {
		return nil, true, ErrNotFound
	}
	if run.Status == StatusRunning && stopped(run, time.Now()) {
		run.Status, run.Error = StatusFailed, "the replica running the draft stopped before it ended"
	}
	return run, true, nil
}

// Begin starts a background draft and renders what run_draft answers: its run
// id and where to read it.
func (l *Launcher) Begin(ctx context.Context, owner, script string, limit time.Duration, exec Execution) (map[string]any, error) {
	id, err := l.Start(ctx, owner, script, limit, exec)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"run_id": id, "status": StatusRunning, "timeout_seconds": int(limit / time.Second),
		"message": fmt.Sprintf("The draft runs in the background for up to %s. Read its log and progress with "+
			"command=get_run run_id=%s; once it ends, get_run returns what run_draft would have, its recording among it.", limit, id),
	}, nil
}

// Answer renders get_run for a background draft. ok is false when the run id
// is not one, so get_run reads its runs as before; err is what the caller is
// told instead of an answer.
func (l *Launcher) Answer(ctx context.Context, id, caller string, admin bool) (out map[string]any, ok bool, err error) {
	run, ok, err := l.Read(ctx, id, caller, admin)
	switch {
	case !ok:
		return nil, false, nil
	case errors.Is(err, ErrNotFound):
		return nil, true, errors.New("run not found")
	case err != nil:
		slog.ErrorContext(ctx, "scripts: reading a background draft failed", "run_id", id, "error", err)
		return nil, true, errors.New("failed to read the run")
	}
	b, _ := json.Marshal(run)
	_ = json.Unmarshal(b, &out)
	out["kind"] = "draft"
	return out, true, nil
}

// stopped reports whether a running draft's replica has stopped: it is past
// its deadline, or has not written the draft for grace.
func stopped(run *Run, now time.Time) bool {
	if now.After(run.Deadline.Add(grace)) {
		return true
	}
	return !run.ReportedAt.IsZero() && now.Sub(run.ReportedAt) > grace
}
