package scriptexec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/script"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// surfaceRunScript is what an approved run records as its tool name in audit,
// alongside the script_run event kind. It names the surface that asked for the
// run, matching how a served prompt records prompts/get or manage_prompt.
const surfaceRunScript = "run_script"

// workerTokenBytes is the entropy in a worker's fencing name.
const workerTokenBytes = 8

// generateWorkerToken returns a random per-process worker name.
func generateWorkerToken() (string, error) {
	b := make([]byte, workerTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a worker id: %w", err)
	}
	return "worker-" + hex.EncodeToString(b), nil
}

// runner executes one approved run end to end.
type runner struct {
	runs   script.RunStore
	server *mcp.Server
	export ExportDeps
	audit  middleware.AuditLogger
}

// newRunner builds the executor the worker drives.
func newRunner(runs script.RunStore, cfg Config) *runner {
	return &runner{runs: runs, server: cfg.Server, export: cfg.Export, audit: cfg.Audit}
}

// execute runs one claimed run: open a session as the script principal, execute
// the approved source under the version's grant, and report the outcome.
func (r *runner) execute(ctx context.Context, run *script.Run, sc *script.Script, v *script.Version) attempt {
	caller, cleanup, err := r.connect(ctx, run, sc, v)
	if err != nil {
		// The session is platform machinery, not the script: failing to open one
		// says nothing about the code and everything about this replica.
		return *retryable("opening the run's platform session failed: " + err.Error())
	}
	defer cleanup()

	grants := v.Grants
	opts := scriptrun.ApprovedLimits()
	opts.Source = v.Source
	opts.Name = sc.Name
	opts.RunID = run.ID
	// The fire time is what the run was created to compute against, not the
	// moment a worker got to it and not the moment it became due again after a
	// retry. A run delayed either way must produce the same report it would have
	// produced on time, or the delay would silently change the answer.
	opts.FireTime = run.FireTime.UTC()
	opts.Params = run.Params
	opts.Caller = caller
	opts.Grants = &grants
	opts.Exporter = r.exporter(run, sc)

	result, runErr := scriptrun.Run(ctx, opts)
	outcome := attemptFrom(result, runErr)
	r.recordAudit(ctx, run, sc, v, outcome.result)
	return outcome
}

// attemptFrom turns an engine result into the attempt the worker resolves.
//
// Every outcome here is terminal. The interpreter has run, which means the
// script may already have queried, exported, or both, and re-running it would
// repeat those effects to chase an error that reproduces exactly.
func attemptFrom(result *scriptrun.Result, runErr error) attempt {
	out := attempt{result: script.RunResult{Status: script.RunStatusSucceeded}}
	if result != nil {
		out.result.Log = result.Log
		out.result.LogTruncated = result.LogTruncated
		out.result.Metrics = script.RunMetrics{
			Steps:      result.Steps,
			DurationMS: result.Duration.Milliseconds(),
			Queries:    result.Queries,
			Exports:    len(result.Exports),
		}
	}
	if runErr != nil {
		out.result.Status = script.RunStatusFailed
		out.result.Error = runErr.Error()
	}
	return out
}

// connect opens the run's in-memory MCP session as the script principal.
//
// Three things are established here and nowhere else:
//
//   - the identity: script:<name>, a principal of its own so every gate, rate
//     limiter, and audit row can separate a governed automation from the person
//     who owns it, with that person's address carried alongside for
//     accountability;
//   - the authority: the roles the approval bound, which are the roles the
//     version's AUTHOR held. The middleware resolves them to a persona exactly
//     as it does for a human caller, so the persona — not the grant struct — is
//     the authority of record, and the grant can only narrow it;
//   - the session: the run id itself, threaded on so all of a run's calls share
//     one session id in audit and none of them touch the owner's own discovery
//     or gate state.
func (r *runner) connect(ctx context.Context, run *script.Run, sc *script.Script, v *script.Version) (*scriptrun.SessionCaller, func(), error) {
	serverCtx := middleware.WithSource(ctx, middleware.SourceScript)
	serverCtx = pkgsession.WithAwareSessionID(serverCtx, run.ID)
	serverCtx = middleware.WithPreAuthenticatedUser(serverCtx, &middleware.UserInfo{
		UserID:   sc.Principal(),
		Email:    sc.OwnerEmail,
		Roles:    v.Grants.Roles,
		AuthType: middleware.AuthTypeScript,
	})
	caller, cleanup, err := scriptrun.Connect(serverCtx, r.server, "script-run")
	if err != nil {
		return nil, nil, fmt.Errorf("opening the run's session: %w", err)
	}
	return caller, cleanup, nil
}

// exporter builds the output writer for one run.
//
// A deployment with no portal asset store or object storage cannot persist an
// output, and an approved run that quietly previewed instead would be recorded
// as a success that wrote nothing — a scheduled report whose asset never
// appears, with a green run behind it. So the missing dependency becomes an
// export that FAILS, which fails the run and says why. (A draft run still
// previews: that is decided by the authoring path, which passes no exporter at
// all.)
func (r *runner) exporter(run *script.Run, sc *script.Script) scriptrun.Exporter {
	if !r.export.ready() {
		slog.Warn("scripts: this deployment cannot persist script outputs; runs that export will fail",
			logKeyRunID, run.ID)
		return unavailableExporter{}
	}
	return newOutputWriter(r.export, r.runs, run, sc)
}

// unavailableExporter refuses every output on a deployment with no asset store
// or object storage configured.
type unavailableExporter struct{}

// Export always fails, naming what the deployment is missing.
func (unavailableExporter) Export(context.Context, scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	return nil, errors.New("this deployment cannot persist script outputs: it has no portal asset store or object storage configured")
}

// recordAudit writes the script_run lifecycle event.
//
// It is one event for the whole run, distinct from the per-capability tool-call
// rows the middleware already writes under the script principal: those say what
// the script reached, this says that the platform executed it, for whom, and how
// it ended. Both carry the run id as their session, so the lifecycle event and
// the calls it covers join on one key. A failure to record is logged and never
// fails the run — audit is off the execution path by design.
func (r *runner) recordAudit(ctx context.Context, run *script.Run, sc *script.Script, v *script.Version, res script.RunResult) {
	if r.audit == nil {
		return
	}
	event := middleware.AuditEvent{
		Timestamp:  time.Now().UTC(),
		SessionID:  run.ID,
		UserID:     sc.Principal(),
		UserEmail:  sc.OwnerEmail,
		ToolName:   surfaceRunScript,
		Success:    res.Status == script.RunStatusSucceeded,
		Authorized: true,
		Source:     middleware.SourceScript,
		EventKind:  string(audit.EventTypeScriptRun),
		DurationMS: res.Metrics.DurationMS,
		Parameters: map[string]any{
			"script":       sc.Name,
			"script_id":    sc.ID,
			"version":      v.Version,
			"run_id":       run.ID,
			"owner":        sc.OwnerEmail,
			"trigger":      run.Trigger,
			"requested_by": run.RequestedBy,
			"attempt":      run.Attempt,
		},
		ErrorMessage: res.Error,
	}
	if err := r.audit.Log(ctx, event); err != nil {
		slog.Warn("scripts: recording the run audit event failed", logKeyRunID, run.ID, logKeyError, err)
	}
}
