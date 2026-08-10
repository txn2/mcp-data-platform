package graphprobe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
)

// ArchiveProbe reports which instrument wrote an archive: probeName for a
// completion run, "" for a lookup-era run (whose manifests predate the probe
// field). Reread dispatches on it.
func ArchiveProbe(dir string) (string, error) {
	var sniff struct {
		Manifest struct {
			Probe string `json:"probe"`
		} `json:"manifest"`
	}
	if err := readArchiveJSON(dir, &sniff); err != nil {
		return "", err
	}
	return sniff.Manifest.Probe, nil
}

// LookupAttempt is one attempt of a lookup-era archive (the retired
// instrument). The shape mirrors what those runs wrote, so their transcripts
// stay re-readable offline.
type LookupAttempt struct {
	CellID      string  `json:"cell_id"`
	Depth       int     `json:"depth"`
	Replicate   int     `json:"replicate"`
	Seq         int     `json:"seq"`
	Email       string  `json:"email"`
	Prompt      string  `json:"prompt"`
	GroundTruth float64 `json:"ground_truth"`
	Unit        string  `json:"unit"`
	WallMS      int64   `json:"wall_ms"`
	ToolCalls   int     `json:"tool_calls"`
	Reading     Reading `json:"reading"`
	Outcome     Outcome `json:"outcome"`
	Error       string  `json:"error,omitempty"`
}

// lookupArchive is the subset of a lookup-era results.json reread needs.
type lookupArchive struct {
	Planted  Planted         `json:"planted"`
	Cells    []graphfix.Cell `json:"cells"`
	Attempts []LookupAttempt `json:"attempts"`
}

// RereadLookup recomputes a lookup-era archive's readings from its
// transcripts, using the current classifier rather than the one the run
// shipped with. Offline: no platform, no key, no network — the transcripts
// are the evidence and the reading is a function of them.
func RereadLookup(dir string) ([]LookupAttempt, error) {
	var res lookupArchive
	if err := readArchiveJSON(dir, &res); err != nil {
		return nil, err
	}
	byID := map[string]graphfix.Cell{}
	for _, c := range res.Cells {
		byID[c.ID] = c
	}
	out := make([]LookupAttempt, 0, len(res.Attempts))
	for _, a := range res.Attempts {
		cell, ok := byID[a.CellID]
		if !ok {
			return nil, fmt.Errorf("graphprobe: archive names cell %q, which its own cell list does not define", a.CellID)
		}
		transcript, err := readTranscript(dir, a.CellID, a.Replicate, a.Error)
		if err != nil {
			return nil, err
		}
		if transcript != nil {
			a.Reading = Read(transcript, cell, res.Planted)
		}
		out = append(out, a)
	}
	return out, nil
}

// RereadCompletion recomputes a completion archive's readings, coverage and
// (for an eliciting run) completeness claims from its transcripts and
// archived final documents, using the current classifier and grader.
//
// Rereads run over the compiled-in fixture corpus, and the archive must
// actually have been produced from it: with generated corpora in play
// (#1250), silently regrading a study archive over the wrong reference graph
// would print coverage that contradicts the run's own numbers. A study
// archive's manifest will carry its generator spec and reread will
// regenerate that corpus instead (#1251).
func RereadCompletion(dir string) (*CompletionResults, error) {
	var res CompletionResults
	if err := readArchiveJSON(dir, &res); err != nil {
		return nil, err
	}
	corpus := graphfix.Default()
	if err := rereadCorpusMatches(res, corpus); err != nil {
		return nil, err
	}
	byID := map[string]graphfix.CompletionCell{}
	for _, c := range res.Cells {
		byID[c.ID] = c
	}
	for i := range res.Attempts {
		if err := rereadAttempt(dir, &res, &res.Attempts[i], corpus, byID); err != nil {
			return nil, err
		}
	}
	return &res, nil
}

// rereadCorpusMatches refuses an archive the fixture corpus cannot vouch
// for: the page count must match the plant and every archived cell must be a
// fixture cell with its fixture entry page.
func rereadCorpusMatches(res CompletionResults, corpus graphfix.Corpus) error {
	if len(res.Planted.Pages) != len(corpus.Pages) {
		return fmt.Errorf("graphprobe: archive planted %d pages but the fixture corpus holds %d; this archive was not produced from the compiled-in fixture and cannot be reread against it", len(res.Planted.Pages), len(corpus.Pages))
	}
	for _, cell := range res.Cells {
		fixture, ok := corpus.CellByID(cell.ID)
		if !ok || fixture.EntryKey != cell.EntryKey {
			return fmt.Errorf("graphprobe: archive cell %q is not the fixture's cell of that id; this archive was not produced from the compiled-in fixture", cell.ID)
		}
	}
	return nil
}

// rereadAttempt recomputes one attempt in place.
func rereadAttempt(dir string, res *CompletionResults, a *CompletionAttempt,
	corpus graphfix.Corpus, byID map[string]graphfix.CompletionCell,
) error {
	cell, ok := byID[a.CellID]
	if !ok {
		return fmt.Errorf("graphprobe: archive names cell %q, which its own cell list does not define", a.CellID)
	}
	transcript, err := readTranscript(dir, a.CellID, a.Replicate, a.Error)
	if err != nil {
		return err
	}
	if transcript == nil {
		return nil
	}
	a.Reading = ReadCompletion(transcript, corpus, cell, res.Planted)
	a.Coverage = GradeCoverage(a.FinalDoc, cell, a.Reading)
	if res.Manifest.ElicitCompleteness {
		claim := ReadCompletenessClaim(a.FinalDoc)
		a.Claim = &claim
		a.Overclaim = Overclaim(a.Coverage, claim)
	}
	return nil
}

// readArchiveJSON decodes a run directory's results.json.
func readArchiveJSON(dir string, v any) error {
	raw, err := os.ReadFile(filepath.Join(dir, "results.json")) // #nosec G304 -- operator-supplied archive path
	if err != nil {
		return fmt.Errorf("graphprobe: reading %s/results.json: %w", dir, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("graphprobe: decoding %s/results.json: %w", dir, err)
	}
	return nil
}

// readTranscript loads one attempt's archived conversation. A missing file is
// not an error for an attempt that failed before it produced one; it is an
// error for any attempt that did.
func readTranscript(dir, cellID string, replicate int, attemptErr string) ([]llm.Message, error) {
	path := filepath.Join(dir, "transcripts", fmt.Sprintf("%s-r%d.json", cellID, replicate))
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied archive path
	if os.IsNotExist(err) {
		if attemptErr != "" {
			return nil, nil
		}
		return nil, fmt.Errorf("graphprobe: %s is missing and its attempt records no error, so its reading cannot be reproduced", path)
	}
	if err != nil {
		return nil, fmt.Errorf("graphprobe: reading %s: %w", path, err)
	}
	var transcript []llm.Message
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return nil, fmt.Errorf("graphprobe: decoding %s: %w", path, err)
	}
	return transcript, nil
}
