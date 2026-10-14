package coldstart

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/catalogref"
	"github.com/txn2/mcp-data-platform/bench/internal/curriculum"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
)

// getEntityToolName reads a dataset's curation through the platform (the same
// read path evaluators have): fetch on its catalog reference, which returns the
// aspects apply_knowledge promotions write.
const getEntityToolName = catalogref.FetchTool

// preflightRemediation tells the operator how to restore a clean baseline:
// every piece of state the preflight checks lives in Postgres, and the
// bench-cold-start make target's TRUNCATE resets all of it.
const preflightRemediation = "remediation: start the run through the bench-cold-start make target, whose TRUNCATE resets the platform state (search gate, memory records, changesets, knowledge pages and the catalog's curation); the undocumented datasets stay, which is the empty baseline (`make bench-seed-catalog-empty` restores any that are missing)"

// preflight refuses to run against a contaminated platform, before any LLM
// episode is spent. An empty baseline cannot be restored by re-seeding (see
// preflightRemediation), and nothing downstream would catch the contamination —
// it would just publish a silently wrong curve (an inflated baseline, an
// attenuated lift). Checked, in order: every lesson entity is undocumented
// (no description, no tags, no properties), no insight of ANY
// status exists at all, and no knowledge page exists at all — the cold-start
// contract is an EMPTY enrichment layer, so the store-level checks assert
// emptiness outright rather than hunting curriculum-shaped leftovers (an S5
// run's lc-* pages or evaluator-identity insights would otherwise slip through).
func (e *runEnv) preflight(ctx context.Context, cur curriculum.Curriculum) error {
	if err := e.preflightEntities(ctx, cur); err != nil {
		return err
	}
	if err := e.preflightInsights(ctx); err != nil {
		return err
	}
	return e.preflightPages(ctx)
}

// preflightEntities checks every distinct lesson entity reads back
// undocumented. The per-entity reads are independent, so they run concurrently.
func (e *runEnv) preflightEntities(ctx context.Context, cur curriculum.Curriculum) error {
	session, handle, err := e.adminSession(ctx)
	if err != nil {
		return fmt.Errorf("preflight admin session: %w", err)
	}
	defer func() { _ = session.Close() }()
	var urns []string
	seen := map[string]bool{}
	for _, l := range cur.Lessons {
		if !seen[l.EntityURN] {
			seen[l.EntityURN] = true
			urns = append(urns, l.EntityURN)
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, len(urns))
	for i, urn := range urns {
		wg.Go(func() {
			errs[i] = e.checkEntityClean(ctx, session, handle, urn)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// checkEntityClean reads one entity through the platform and reports the first
// contamination it carries.
func (e *runEnv) checkEntityClean(ctx context.Context, session *mcp.ClientSession, handle, urn string) error {
	res := mcpc.Call(ctx, session, getEntityToolName, catalogref.FetchArgs(urn), handle)
	if res.TransportErr != nil {
		return fmt.Errorf("preflight %s(%s): %w", getEntityToolName, urn, res.TransportErr)
	}
	if res.ToolErr {
		return fmt.Errorf("preflight %s(%s) failed: %.300s (an unreadable baseline cannot be verified clean)", getEntityToolName, urn, res.Text)
	}
	entity, err := catalogref.Parse(res.Text)
	if err != nil {
		return fmt.Errorf("preflight %s(%s): %w", getEntityToolName, urn, err)
	}
	if reason := entityContamination(entity); reason != "" {
		return fmt.Errorf("baseline contaminated: entity %s %s; %s", urn, reason, preflightRemediation)
	}
	return nil
}

// entityContamination reports why an entity is not an empty-baseline entity, or
// "" when it is clean.
func entityContamination(entity catalogref.Curation) string {
	switch {
	case strings.TrimSpace(entity.Description) != "":
		return fmt.Sprintf("already carries a description (%.120q), left by a prior run's promotion or an a2 seed", entity.Description)
	case len(entity.Tags) > 0:
		return "carries tags left by an a2 seed"
	case len(entity.CustomProperties) > 0:
		return "carries properties left by an a2 seed"
	}
	return ""
}

// preflightInsights checks the insight store is EMPTY (any capturer, any
// status, any entity). The baseline is an empty enrichment layer, so any
// insight at all — this suite's deterministic teacher identities, an S5 run's
// teachers or learners — is a leftover that would distort the measured state.
// One unfiltered list is both one round-trip and strictly stronger than
// per-pair queries.
func (e *runEnv) preflightInsights(ctx context.Context) error {
	insights, err := e.life.ListInsights(ctx, lifecycleapi.InsightFilter{})
	if err != nil {
		return fmt.Errorf("preflight list insights: %w", err)
	}
	if len(insights) > 0 {
		return fmt.Errorf("baseline contaminated: %d insight(s) already exist (the cold-start baseline requires an empty insight store); %s",
			len(insights), preflightRemediation)
	}
	return nil
}

// preflightPages checks no knowledge page exists at all — the run boots with
// BENCH_SEED_PAGES=0, so any page (a curriculum slug, an S5 lc-* definition
// page) is a leftover the eval could read.
func (e *runEnv) preflightPages(ctx context.Context) error {
	pages, err := e.life.ListKnowledgePages(ctx)
	if err != nil {
		return fmt.Errorf("preflight list knowledge pages: %w", err)
	}
	if len(pages) > 0 {
		return fmt.Errorf("baseline contaminated: %d knowledge page(s) already exist (first: %q; the cold-start baseline requires none); %s",
			len(pages), pages[0].Slug, preflightRemediation)
	}
	return nil
}
