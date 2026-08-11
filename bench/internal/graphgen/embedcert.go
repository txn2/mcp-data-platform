package graphgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// Authoring-time discontinuity certification (#1250), the first of the two
// gates: before a corpus is ever planted, every discontinuity constraint's
// source page must sit outside the top-K corpus pages by embedding
// similarity to every task-derived phrasing, while the cell's entry page
// stays findable. This is the offline analog of the live sweep gate — same
// statistic (rank of a page against a task phrasing), independent instrument
// (raw cosine over whole pages here; the platform's chunked index and its
// own ranking there) — so a discontinuity claim has to survive two different
// rankers before an episode is spent on it.

// Embedder produces one embedding per input text. OllamaEmbedder implements
// it against the same local model the study platform embeds with.
type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)
}

// OllamaEmbedder embeds through a local ollama server's batch endpoint,
// mirroring the platform's own provider (pkg/embedding: /api/embed with the
// model name, no instruction prefixes).
type OllamaEmbedder struct {
	BaseURL string
	Model   string
	HTTP    *http.Client
}

// embedBatchSize bounds one ollama request's input list.
const embedBatchSize = 64

// EmbedBatch embeds texts in bounded batches.
func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	out := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatchSize {
		batch := texts[start:min(start+embedBatchSize, len(texts))]
		got, err := o.embedOnce(ctx, client, batch)
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// embedOnce performs one /api/embed call.
func (o *OllamaEmbedder) embedOnce(ctx context.Context, client *http.Client, texts []string) ([][]float64, error) {
	payload, err := json.Marshal(map[string]any{"model": o.Model, "input": texts})
	if err != nil {
		return nil, fmt.Errorf("graphgen: marshal embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("graphgen: build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphgen: embed call: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("graphgen: reading embed response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphgen: embed call: status %d: %.300s", res.StatusCode, raw)
	}
	var body struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("graphgen: decoding embed response: %w", err)
	}
	if len(body.Embeddings) != len(texts) {
		return nil, fmt.Errorf("graphgen: embed returned %d embeddings for %d texts", len(body.Embeddings), len(texts))
	}
	return body.Embeddings, nil
}

// CertPhrasing is one (cell, phrasing) certification reading.
type CertPhrasing struct {
	CellID   string `json:"cell_id"`
	Phrasing string `json:"phrasing"`
	// Prompt marks the cell's prompt itself (the first phrasing); the
	// entry-findability requirement is read on it.
	Prompt bool `json:"prompt,omitempty"`
	// EntryRank is the entry page's 1-based rank among all corpus pages by
	// cosine similarity, and EntrySim its similarity.
	EntryRank int     `json:"entry_rank"`
	EntrySim  float64 `json:"entry_sim"`
	// ConstraintRanks is the enumeration profile: every constraint source
	// page's rank against this phrasing.
	ConstraintRanks map[string]int `json:"constraint_ranks"`
	// DiscontinuityViolations are the discontinuity source pages inside the
	// top-K, each with its rank. Any entry fails the certification.
	DiscontinuityViolations map[string]int `json:"discontinuity_violations,omitempty"`
	Pass                    bool           `json:"pass"`
}

// CertReport is the whole authoring-time certification for one corpus.
type CertReport struct {
	RanAt time.Time `json:"ran_at"`
	Spec  Spec      `json:"spec"`
	Model string    `json:"model"`
	// TopK is the exclusion horizon for discontinuity pages; EntryTopK is
	// the findability horizon for entry pages.
	TopK      int            `json:"top_k"`
	EntryTopK int            `json:"entry_top_k"`
	Phrasings []CertPhrasing `json:"phrasings"`
	// HorizonExceedsCorpus marks a horizon covering at least half the
	// corpus: exclusion stops meaning anything when one practical result
	// list reaches most of the store. That is the within-enumeration-ceiling
	// condition the probe measured, not an authoring failure; the report
	// records it so a failed reading at the smallest scale is read as the
	// scale axis working.
	HorizonExceedsCorpus bool `json:"horizon_exceeds_corpus,omitempty"`
	Pass                 bool `json:"pass"`
}

// CertTopK and CertEntryTopK bound the frozen horizons: exclusion never
// wider than the top 100 (the widest limit an episode was ever observed to
// ask for), entry findable in the top 25 (the modal limit).
const (
	CertTopK      = 100
	CertEntryTopK = 25
)

// EffectiveTopK is the exclusion horizon for a corpus of n pages: two
// percent of the corpus, never below the modal episode limit and never
// above the widest swept one. The horizon models the largest result list an
// episode practically consumes; a fixed 100 would demand exclusion from the
// top fifth of a 500-page corpus while demanding only the top fiftieth of a
// 5000-page one — ten times stricter exactly where the scale axis says the
// pressure is lower — so the horizon scales with the corpus instead.
func EffectiveTopK(n int) int {
	return min(max(n/50, CertEntryTopK), CertTopK)
}

// WithinCeiling reports whether a corpus of n pages sits within the
// enumeration ceiling: the exclusion horizon covers at least half the store,
// so one practical result list reaches most of it and discontinuity cannot
// exist by construction (#1250). At that scale certification is unsatisfiable,
// the sweep gate records discontinuity hits on purpose, and episodes run the
// cells with the discontinuity constraints graded as ordinary spread
// constraints — which, at that scale, is what they are.
func WithinCeiling(n int) bool {
	return withinHorizon(EffectiveTopK(n), n)
}

// withinHorizon is the shared rule: a horizon of topK covers at least half a
// corpus of n pages.
func withinHorizon(topK, n int) bool {
	return 2*topK >= n
}

// CertifyDiscontinuity runs the authoring-time gate over a generated corpus.
func CertifyDiscontinuity(ctx context.Context, emb Embedder, res *Result, topK, entryTopK int) (*CertReport, error) {
	report := &CertReport{
		RanAt: time.Now().UTC(), Spec: res.Spec, TopK: topK, EntryTopK: entryTopK, Pass: true,
		HorizonExceedsCorpus: withinHorizon(topK, len(res.Corpus.Pages)),
	}
	if o, ok := emb.(*OllamaEmbedder); ok {
		report.Model = o.Model
	}
	pages := res.Corpus.Sorted()
	pageVecs, err := emb.EmbedBatch(ctx, pageTexts(pages))
	if err != nil {
		return nil, err
	}
	for _, cell := range res.Corpus.Cells {
		phrasings := append([]string{cell.Prompt}, cell.GateQueries...)
		queryVecs, err := emb.EmbedBatch(ctx, phrasings)
		if err != nil {
			return nil, err
		}
		for i, phrasing := range phrasings {
			p := certifyPhrasing(cell, phrasing, i == 0, pages, pageVecs, queryVecs[i], topK, entryTopK)
			report.Phrasings = append(report.Phrasings, p)
			if !p.Pass {
				report.Pass = false
			}
		}
	}
	return report, nil
}

// pageTexts renders what each page is embedded as: title, summary and the
// stripped body — the arm-neutral rendering, so certification never depends
// on which arm gets planted.
func pageTexts(pages []graphfix.Page) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Title + "\n" + p.Summary + "\n" + p.StrippedBody()
	}
	return out
}

// certifyPhrasing ranks the corpus against one phrasing and applies the
// requirements: discontinuity pages outside topK always, entry inside
// entryTopK on the prompt phrasing.
func certifyPhrasing(cell graphfix.CompletionCell, phrasing string, isPrompt bool,
	pages []graphfix.Page, pageVecs [][]float64, queryVec []float64, topK, entryTopK int,
) CertPhrasing {
	out := CertPhrasing{
		CellID: cell.ID, Phrasing: phrasing, Prompt: isPrompt,
		ConstraintRanks: map[string]int{},
	}
	ranked := rankBySimilarity(pages, pageVecs, queryVec)
	setPages := cell.AllConstraintPages()
	discPages := cell.DiscontinuityPages()
	for rank, entry := range ranked {
		key := entry.key
		if key == cell.EntryKey {
			out.EntryRank, out.EntrySim = rank+1, entry.sim
		}
		if slices.Contains(setPages, key) {
			out.ConstraintRanks[key] = rank + 1
		}
		if slices.Contains(discPages, key) && rank+1 <= topK {
			if out.DiscontinuityViolations == nil {
				out.DiscontinuityViolations = map[string]int{}
			}
			out.DiscontinuityViolations[key] = rank + 1
		}
	}
	out.Pass = len(out.DiscontinuityViolations) == 0 && (!isPrompt || (out.EntryRank > 0 && out.EntryRank <= entryTopK))
	return out
}

// rankedPage pairs a page key with its similarity to one query.
type rankedPage struct {
	key string
	sim float64
}

// rankBySimilarity orders all pages by descending cosine similarity.
func rankBySimilarity(pages []graphfix.Page, pageVecs [][]float64, queryVec []float64) []rankedPage {
	out := make([]rankedPage, len(pages))
	for i, p := range pages {
		out[i] = rankedPage{key: p.Key, sim: cosine(pageVecs[i], queryVec)}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].sim > out[j].sim })
	return out
}

// cosine returns the cosine similarity of two vectors, 0 on any mismatch.
func cosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
