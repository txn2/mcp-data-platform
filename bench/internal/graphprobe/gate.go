package graphprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// searchTool is the platform's discovery entry point.
const searchTool = "search"

// GateLimits are the search limits the gate sweeps. The lookup-shaped
// attempt's gate ran once at the tool default and the episodes defeated it on
// their first call, because the agent chooses its own limit: opus asked for
// 25 on 65 of its 67 searches, haiku ranged from 1 to 100, and one call
// returned the entire corpus. The sweep brackets that observed range.
var GateLimits = []int{5, 25, 100}

// entryGateLimit is the limit the entry-surfacing precondition is read at:
// the modal limit episodes actually use.
const entryGateLimit = 25

// GateResult is one (cell, query, limit) sweep reading.
type GateResult struct {
	CellID string `json:"cell_id"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	// EntryRank is the entry page's 1-based position among the knowledge-page
	// hits, or 0 when it did not surface.
	EntryRank int `json:"entry_rank"`
	// PageRanks maps each constraint-set page that surfaced to its 1-based
	// rank. This is the enumeration profile: how reachable the spread mass is
	// to a query that cannot name it. Recorded, not failed on — reachability
	// at some limit is the economics test's subject, not a leak.
	PageRanks map[string]int `json:"page_ranks,omitempty"`
	// Leaks are the off-entry constraint ids whose signature appeared in a
	// hit's rendered text. A leak is a fact search delivers without any read,
	// and it fails the gate.
	Leaks []string `json:"leaks,omitempty"`
	// Hits is every knowledge-page hit in rank order, as fixture keys; a hit
	// outside the fixture is recorded by its reference.
	Hits []string `json:"hits"`
	Pass bool     `json:"pass"`
}

// GateReport is the whole sweep for one plant.
type GateReport struct {
	RanAt time.Time `json:"ran_at"`
	// Stripped records which arm's plant was swept, from the plant record.
	Stripped bool         `json:"stripped,omitempty"`
	Limits   []int        `json:"limits"`
	Results  []GateResult `json:"results"`
	// Pass is true only when every sweep reading passed and every cell's
	// entry page surfaced for its prompt-derived query at the modal limit.
	Pass bool `json:"pass"`
}

// Gate runs the pre-stated fixture gate: every cell's authored queries, swept
// across the limits an agent actually uses, through the platform's own
// `search` tool as a fresh pool identity, so what the gate sees is what an
// episode sees.
//
// Two conditions gate a run. No off-entry constraint signature may appear in
// any hit's rendered text at any swept combination, or grounded coverage
// stops meaning anything. And each cell's entry page must surface for its
// prompt-derived query at the modal episode limit, or the search arms have no
// entry point. Constraint pages surfacing is recorded as the enumeration
// profile rather than failed on.
func Gate(ctx context.Context, t target.Target, identityKeys int, planted Planted, timeout time.Duration) (GateReport, error) {
	report := GateReport{RanAt: time.Now().UTC(), Stripped: planted.Stripped, Limits: slices.Clone(GateLimits), Pass: true}
	seq := 0
	for _, cell := range graphfix.CompletionCells() {
		if err := gateCell(ctx, t, identityKeys, planted, cell, &seq, &report, timeout); err != nil {
			return report, err
		}
	}
	return report, nil
}

// gateCell sweeps one cell's queries across the gate limits, folding each
// reading into the report.
func gateCell(ctx context.Context, t target.Target, identityKeys int, planted Planted,
	cell graphfix.CompletionCell, seq *int, report *GateReport, timeout time.Duration,
) error {
	for qi, query := range cell.GateQueries {
		for _, limit := range GateLimits {
			*seq++
			res, err := gateSweep(ctx, t, identityKeys, planted, cell, query, limit, *seq, timeout)
			if err != nil {
				return err
			}
			report.Results = append(report.Results, res)
			if !res.Pass || (qi == 0 && limit == entryGateLimit && res.EntryRank == 0) {
				report.Pass = false
			}
		}
	}
	return nil
}

// gateSweep runs one (cell, query, limit) reading.
func gateSweep(ctx context.Context, t target.Target, identityKeys int, planted Planted,
	cell graphfix.CompletionCell, query string, limit, seq int, timeout time.Duration,
) (GateResult, error) {
	res := GateResult{CellID: cell.ID, Query: query, Limit: limit}
	hits, err := gateSearch(ctx, t, identityKeys, cell.ID, query, limit, seq, timeout)
	if err != nil {
		return res, err
	}
	res.read(hits, cell, planted)
	return res, nil
}

// gateSearch runs one query through the platform's own search tool as a pool
// identity.
func gateSearch(ctx context.Context, t target.Target, identityKeys int,
	cellID, query string, limit, seq int, timeout time.Duration,
) ([]searchHit, error) {
	cred := pool.Credential(t.Credential, seq, identityKeys)
	client := mcpc.New(t.BaseURL, target.Target{BaseURL: t.BaseURL, Credential: cred}.HTTPClient(timeout))
	session, err := client.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("graphprobe: gate connect for %s: %w", cellID, err)
	}
	defer func() { _ = session.Close() }()
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("graphprobe: gate handle for %s: %w", cellID, err)
	}
	call := mcpc.Call(ctx, session, searchTool, map[string]any{"intent": query, "limit": limit}, info.Handle)
	switch {
	case call.TransportErr != nil:
		return nil, fmt.Errorf("graphprobe: gate search for %s: %w", cellID, call.TransportErr)
	case call.ToolErr:
		return nil, fmt.Errorf("graphprobe: gate search for %s refused: %.300s", cellID, call.Text)
	}
	hits, err := knowledgePageHits(call.Text)
	if err != nil {
		return nil, fmt.Errorf("graphprobe: gate search for %s: %w", cellID, err)
	}
	return hits, nil
}

// read records what one sweep query returned and applies the leak condition.
func (r *GateResult) read(hits []searchHit, cell graphfix.CompletionCell, planted Planted) {
	setPages := cell.AllConstraintPages()
	leaks := map[string]bool{}
	for rank, hit := range hits {
		r.readHit(hit, rank+1, cell, setPages, planted, leaks)
	}
	for id := range leaks {
		r.Leaks = append(r.Leaks, id)
	}
	slices.Sort(r.Leaks)
	r.Pass = len(r.Leaks) == 0
}

// readHit folds one ranked hit into the reading.
func (r *GateResult) readHit(hit searchHit, rank int, cell graphfix.CompletionCell,
	setPages []string, planted Planted, leaks map[string]bool,
) {
	key, known := planted.KeyForReference(hit.Reference)
	if !known {
		key = hit.Reference
	}
	r.Hits = append(r.Hits, key)
	if key == cell.EntryKey && r.EntryRank == 0 {
		r.EntryRank = rank
	}
	if slices.Contains(setPages, key) {
		if r.PageRanks == nil {
			r.PageRanks = map[string]int{}
		}
		if _, seen := r.PageRanks[key]; !seen {
			r.PageRanks[key] = rank
		}
	}
	for _, id := range cell.SignatureLeaks(hit.Text) {
		leaks[id] = true
	}
}

// searchHit is the subset of a search hit the gate reads.
type searchHit struct {
	Text      string `json:"text"`
	Reference string `json:"reference"`
}

// searchPayload is the subset of the search tool's structured output the gate
// reads: the knowledge-page group's hits, in the order the platform ranked them.
type searchPayload struct {
	Groups []struct {
		Source string      `json:"source"`
		Hits   []searchHit `json:"hits"`
	} `json:"groups"`
}

// knowledgePageHits extracts the knowledge-page hits from a search result.
func knowledgePageHits(text string) ([]searchHit, error) {
	var payload searchPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("decoding search result: %w", err)
	}
	for _, g := range payload.Groups {
		if g.Source == "knowledge_pages" {
			return g.Hits, nil
		}
	}
	return nil, nil
}
