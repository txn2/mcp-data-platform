// Command pollutionplant drives the knowledge-pollution study's stack-side
// steps (#1165): it plants a treatment claim into the shared applied tier,
// remediates a planted claim by supersede or rollback, and prints the
// study's fixture-computed attribution table.
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
	tasksDir    string
	identityKey int
	teacherSeq  int
	witnessSeq  int
	timeout     time.Duration
}

func main() {
	var cfg config
	flag.StringVar(&cfg.mode, "mode", "plant", "plant, remediate, table, or check")
	flag.StringVar(&cfg.url, "url", "http://localhost:8098", "platform base URL (MCP + admin REST)")
	flag.StringVar(&cfg.credential, "credential", os.Getenv("BENCH_CREDENTIAL"), "admin API key (base credential for the identity pool)")
	flag.StringVar(&cfg.treatmentID, "treatment", "", "treatment id to plant or remediate (see -mode table)")
	flag.StringVar(&cfg.remediation, "remediation", string(pollutionplant.RemediationRollback), "supersede or rollback (with -mode remediate)")
	flag.StringVar(&cfg.plantedFile, "planted", "", "path to the plant result JSON (with -mode remediate)")
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
	case "plant", "remediate":
		return runLive(cfg)
	default:
		return fmt.Errorf("unknown -mode %q", cfg.mode)
	}
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

// runLive executes a mode that needs the platform.
func runLive(cfg config) error {
	if cfg.credential == "" {
		return errors.New("-credential (or BENCH_CREDENTIAL) is required")
	}
	tr, err := pollutionplant.TreatmentByID(cfg.treatmentID)
	if err != nil {
		return err
	}
	tgt := target.Target{BaseURL: cfg.url, Credential: cfg.credential}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	client := pollutionplant.New(tgt, cfg.identityKey,
		lifecycleapi.New(cfg.url, tgt.HTTPClient(httpTimeout)), httpTimeout, log)

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
	return writeJSON(res)
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
	return writeJSON(res)
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

// writeJSON emits a result as indented JSON on stdout.
func writeJSON(v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	fmt.Println(string(raw))
	return nil
}
