package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

// supersedeIdentitiesPerRun is the identities one supersede attempt consumes.
// Unlike the full lifecycle it needs only the teacher (teach + correct + recall
// are all the same identity); there is no cross-identity transfer stage. Each
// attempt still uses a fresh identity so an earlier attempt's live insight on
// the same pool identity never confuses the supersede-status read-back.
const supersedeIdentitiesPerRun = 1

// RunSupersede runs the targeted supersede sub-benchmark (issue #964): the
// recall-first supersede gate measured in isolation. For each supersede protocol
// (teach then correct, no promote/transfer) it drives teach -> capture-verify ->
// correct -> supersede-status check, k times, and reports how reliably the
// restatement supersedes the original rather than leaving a duplicate. Isolating
// supersede from the full lifecycle removes the transfer/abstain/promote noise
// that made the S5 duplicate rate a wide range (0% vs 42.9% between identical
// runs), and lets the embedding-similarity gate's stability be evaluated on its
// own — re-run against platforms configured with different similarity
// thresholds, embedding models, or a deterministic fallback to compare.
func RunSupersede(ctx context.Context, opts Options) (*SupersedeResults, error) {
	all, err := protocol.Load(opts.ProtocolsDir)
	if err != nil {
		return nil, err
	}
	protocols := supersedeProtocols(all)
	if len(protocols) == 0 {
		return nil, fmt.Errorf("no supersede protocols (with an update stage) in %s", opts.ProtocolsDir)
	}
	if opts.IdentityKeys <= 0 {
		return nil, errors.New("supersede requires an identity pool (-identity-keys > 0): each attempt teaches under a fresh identity")
	}
	need := len(protocols) * opts.K * supersedeIdentitiesPerRun
	if need > opts.IdentityKeys {
		return nil, fmt.Errorf("%d identities needed (%d supersede protocols x k=%d) exceed the pool of %d; raise -identity-keys and the config pool",
			need, len(protocols), opts.K, opts.IdentityKeys)
	}

	res := &SupersedeResults{Manifest: newManifest(opts, protocols)}
	env := newRunEnv(opts)

	failures := env.runAllSupersede(ctx, protocols, res)
	env.finishManifest(&res.Manifest)
	res.Aggregate()
	if failures > 0 {
		return res, fmt.Errorf("%d supersede run(s) failed at the harness level; see runs[].error", failures)
	}
	return res, nil
}

// supersedeProtocols keeps only the protocols that exercise supersede (an update
// stage). Transfer protocols are excluded by construction — protocol.Validate
// makes update and transfer mutually exclusive.
func supersedeProtocols(all []protocol.Protocol) []protocol.Protocol {
	out := make([]protocol.Protocol, 0, len(all))
	for _, p := range all {
		if p.Update != nil {
			out = append(out, p)
		}
	}
	return out
}

// runAllSupersede executes every supersede protocol k times, appending each run
// to res and returning the count of harness-level failures.
func (e *runEnv) runAllSupersede(ctx context.Context, protocols []protocol.Protocol, res *SupersedeResults) int {
	failures := 0
	attemptIndex := 0
	for _, p := range protocols {
		for attempt := 1; attempt <= e.opts.K; attempt++ {
			run := e.runSupersedeProtocol(ctx, p, attempt, attemptIndex)
			if run.Error != "" {
				failures++
			}
			res.Runs = append(res.Runs, run)
			attemptIndex++
			e.checkpointSupersede(res)
		}
	}
	return failures
}

// checkpointSupersede flushes the results so far after each attempt so an
// interruption never discards completed, paid-for work.
func (e *runEnv) checkpointSupersede(res *SupersedeResults) {
	if e.opts.OnSupersede == nil {
		return
	}
	res.Aggregate()
	e.opts.OnSupersede(res)
}

// runSupersedeProtocol drives one supersede attempt: teach and verify capture,
// then supersede (correct the fact and re-check the taught insight's status). It
// reuses the full lifecycle's teach/capture and supersede stages so the isolated
// sub-benchmark and the S5 suite exercise the identical, correctness-critical
// code — only the surrounding stages differ.
func (e *runEnv) runSupersedeProtocol(ctx context.Context, p protocol.Protocol, attempt, attemptIndex int) ProtocolRun {
	e.currentProtocolID = p.ID
	run := ProtocolRun{ProtocolID: p.ID, Title: p.Title, Sink: p.Sink, Attempt: attempt}
	teacherSeq := attemptIndex*supersedeIdentitiesPerRun + 1

	if abort := e.teachAndCapture(ctx, p, teacherSeq, &run); abort {
		return run
	}
	if !boolTrue(run.Captured) || run.InsightID == "" {
		return run // capture missed: supersede is not applicable, excluded from the rate
	}
	e.supersede(ctx, p, teacherSeq, &run)
	return run
}
