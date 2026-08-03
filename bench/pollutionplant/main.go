// Command pollutionplant drives the knowledge-pollution study's stack-side
// steps (#1165): it plants a treatment claim into the shared applied tier,
// remediates a planted claim by supersede or rollback, snapshots the shared
// store so an arm's constancy can be proved rather than assumed (#1167), and
// prints the study's fixture-computed attribution table.
//
// The evaluation arms themselves are plain benchrun runs over the committed
// tasks; this command is what changes the stack between them.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/pollutionplant"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// httpTimeout bounds each MCP and admin-REST call.
const httpTimeout = 60 * time.Second

// config is the command's parsed flags.
type config struct {
	mode        string
	url         string
	credential  string
	treatmentID string
	remediation string
	plantedFile string
	baseline    string
	tasksDir    string
	identityKey int
	teacherSeq  int
	witnessSeq  int
	timeout     time.Duration
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "plant", "plant, remediate, seed-correct-source, store-state, table, or check")
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform base URL (MCP + admin REST)")
	flag.StringVar(&cfg.credential, "credential", os.Getenv("BENCH_CREDENTIAL"), "admin API key (base credential for the identity pool)")
	flag.StringVar(&cfg.treatmentID, "treatment", "", "treatment id to plant or remediate (see -mode table)")
	flag.StringVar(&cfg.remediation, "remediation", string(pollutionplant.RemediationRollback), "supersede or rollback (with -mode remediate)")
	flag.StringVar(&cfg.plantedFile, "planted", "", "path to the plant result JSON (with -mode remediate)")
	flag.StringVar(&cfg.baseline, "baseline", "", "path to the pre-arm store snapshot (with -mode store-state): compare against it and exit nonzero if the shared store moved during the arm")
	flag.StringVar(&cfg.tasksDir, "tasks", "tasks", "committed task directory (with -mode check)")
	flag.IntVar(&cfg.identityKey, "identity-keys", pool.Size, "identity pool size matching the arm config")
	flag.IntVar(&cfg.teacherSeq, "teacher-seq", 200, "pool identity that captures the claim; keep clear of the evaluation attempts")
	flag.IntVar(&cfg.witnessSeq, "witness-seq", 201, "pool identity the cross-identity read-back runs as")
	flag.DurationVar(&cfg.timeout, "timeout", 5*time.Minute, "overall deadline")
	flag.Parse()

	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "pollutionplant:", err)
		os.Exit(1)
	}
}

// run dispatches on the mode.
func run(cfg config) error {
	switch cfg.mode {
	case "table":
		return printTable()
	case "check":
		return pollutionplant.CheckAgainstFixtures(cfg.tasksDir)
	case "store-state":
		return runStoreState(cfg)
	case "seed-correct-source":
		return runSeed(cfg)
	case "plant", "remediate":
		return runLive(cfg)
	default:
		return fmt.Errorf("unknown -mode %q", cfg.mode)
	}
}

// runSeed creates the API fixture's co-present correct source, which the
// cross-fixture arm's conflict framing depends on (protocol 5.3). It runs
// before the plant on an API-fixture stack, and on no other: the
// perishable-knowledge study's control cell requires this convention to be
// undiscoverable.
func runSeed(cfg config) error {
	client, err := liveClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	return seedCorrectSource(ctx, client, os.Stdout)
}

// seeder creates the API fixture's correct source. It is an interface so the
// mode's behavior is exercisable without a live stack.
type seeder interface {
	SeedCorrectSource(ctx context.Context) (lifecycleapi.KnowledgePage, error)
}

// seedCorrectSource seeds the page and archives what actually landed, which
// is the form the arm's episodes will meet.
func seedCorrectSource(ctx context.Context, s seeder, out io.Writer) error {
	page, err := s.SeedCorrectSource(ctx)
	if err != nil {
		return err
	}
	return writeJSON(out, page)
}

// runStoreState snapshots the shared store and, when a pre-arm snapshot is
// supplied, holds the arm to the constancy invariant (protocol 7.3).
func runStoreState(cfg config) error {
	client, err := liveClient(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	return checkStoreState(ctx, client, cfg.baseline, os.Stdout, os.Stderr)
}

// snapshotter reads the shared store. It is an interface for the same reason
// seeder is: the invariant's decision is the part worth testing, and it must
// not need a platform to exercise.
type snapshotter interface {
	ReadStoreState(ctx context.Context) (pollutionplant.StoreState, error)
}

// checkStoreState writes the snapshot and, given a pre-arm one, reports every
// difference and fails when there is any. The snapshot is archived either
// way, so an arm that drifted still leaves the evidence of what it drifted
// to rather than only the news that it did.
func checkStoreState(ctx context.Context, s snapshotter, baseline string, out, status io.Writer) error {
	state, err := s.ReadStoreState(ctx)
	if err != nil {
		return err
	}
	if err := writeJSON(out, state); err != nil {
		return err
	}
	if baseline == "" {
		return nil
	}
	before, err := readStoreState(baseline)
	if err != nil {
		return err
	}
	drift := before.Drift(state)
	if len(drift) == 0 {
		_, _ = fmt.Fprintln(status, "store state constant across the arm")
		return nil
	}
	for _, line := range drift {
		_, _ = fmt.Fprintln(status, "drift:", line)
	}
	return fmt.Errorf("the shared store changed during the arm (%d difference(s) above); "+
		"the arm's later episodes met a different store than its earlier ones and it must be re-run on a fresh database", len(drift))
}

// readStoreState loads a snapshot taken before the arm.
func readStoreState(path string) (pollutionplant.StoreState, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path to this run's own artifact
	if err != nil {
		return pollutionplant.StoreState{}, fmt.Errorf("read %s: %w", path, err)
	}
	var s pollutionplant.StoreState
	if err := json.Unmarshal(raw, &s); err != nil {
		return pollutionplant.StoreState{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

// printTable writes the attribution table the protocol and report quote.
func printTable() error {
	table, err := pollutionplant.AttributionTable()
	if err != nil {
		return err
	}
	fmt.Print(table)
	return nil
}

// liveClient builds the platform client every stack-side mode shares.
func liveClient(cfg config) (*pollutionplant.Client, error) {
	if cfg.credential == "" {
		return nil, errors.New("-credential (or BENCH_CREDENTIAL) is required")
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return pollutionplant.New(tgt, cfg.identityKey,
		lifecycleapi.New(cfg.url, tgt.HTTPClient(httpTimeout)), httpTimeout, log), nil
}

// runLive executes a mode that acts on one treatment.
func runLive(cfg config) error {
	client, err := liveClient(cfg)
	if err != nil {
		return err
	}
	tr, err := pollutionplant.TreatmentByID(cfg.treatmentID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	if cfg.mode == "plant" {
		return plant(ctx, client, cfg, tr)
	}
	return remediate(ctx, client, cfg, tr)
}

// plant plants one treatment and writes its result to stdout, which a run
// script captures for the later remediation step.
func plant(ctx context.Context, client *pollutionplant.Client, cfg config, tr pollutionplant.Treatment) error {
	res, err := client.Plant(ctx, pollutionplant.Request{
		Treatment:  tr,
		TeacherSeq: cfg.teacherSeq,
		WitnessSeq: cfg.witnessSeq,
	})
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, res)
}

// remediate retracts a previously planted claim.
func remediate(ctx context.Context, client *pollutionplant.Client, cfg config, tr pollutionplant.Treatment) error {
	planted, err := readPlanted(cfg.plantedFile)
	if err != nil {
		return err
	}
	req := pollutionplant.RemediateRequest{
		Remediation: pollutionplant.Remediation(cfg.remediation),
		Planted:     planted,
		Treatment:   tr,
		TeacherSeq:  cfg.teacherSeq,
		WitnessSeq:  cfg.witnessSeq,
	}
	if req.Remediation == pollutionplant.RemediationSupersede {
		correction, err := pollutionplant.Counterpart(tr)
		if err != nil {
			return err
		}
		req.Correction = correction
	}
	res, err := client.Remediate(ctx, req)
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, res)
}

// readPlanted loads the plant result the remediation acts on.
func readPlanted(path string) (pollutionplant.Result, error) {
	if path == "" {
		return pollutionplant.Result{}, errors.New("-planted (the plant result JSON) is required for -mode remediate")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path to this run's own artifact
	if err != nil {
		return pollutionplant.Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	var res pollutionplant.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return pollutionplant.Result{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return res, nil
}

// writeJSON emits a result as indented JSON. Every mode writes its result
// this way, so a run script captures the same shape whatever step it ran.
func writeJSON(w io.Writer, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	_, err = fmt.Fprintln(w, string(raw))
	return err
}
