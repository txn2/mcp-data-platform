// Package graphprobe drives the graph-traversal premise probe (#1241): it
// plants the seeded page corpus through the platform's own knowledge-page API,
// runs the pre-stated fixture gate over `search`, executes the depth cells, and
// archives what each episode read.
//
// The plant goes through the REST create path rather than direct SQL because
// that path is what builds a page's inline reference set
// (Handler.reconcileInlineRefs over knowledgepage.ScanBodyRefs). A page inserted
// straight into the table carries reference tokens in its markdown and no rows
// in the reference table, so `fetch` returns it with an empty `references`
// array and the probe would measure a corpus that has no edges.
package graphprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/graphfix"
)

// plantWorkers bounds the concurrent REST calls a plant, verify or reset
// issues. The platform serves them comfortably; the bound exists so a
// study-scale corpus cannot open thousands of sockets at once.
const plantWorkers = 8

// workGroup runs tasks on a bounded worker pool, keeps the first error, and
// stops running tasks once one has failed: against a live store, every task
// is a write, and writes issued after a failure are work the error return is
// about to disown.
type workGroup struct {
	wg     sync.WaitGroup
	slot   chan struct{}
	once   sync.Once
	failed atomic.Bool
	err    error
}

// newWorkGroup returns a group running at most n tasks concurrently.
func newWorkGroup(n int) *workGroup {
	return &workGroup{slot: make(chan struct{}, n)}
}

// Go schedules one task. Tasks scheduled after a failure are skipped.
func (g *workGroup) Go(task func() error) {
	g.wg.Add(1)
	g.slot <- struct{}{}
	go func() {
		defer func() { <-g.slot; g.wg.Done() }()
		if g.failed.Load() {
			return
		}
		if err := task(); err != nil {
			g.once.Do(func() { g.err = err })
			g.failed.Store(true)
		}
	}()
}

// Wait blocks until every scheduled task finished and returns the first error.
func (g *workGroup) Wait() error {
	g.wg.Wait()
	return g.err
}

// knowledgePageRefType is the reference target type the refs endpoint reports
// for a page-to-page edge (knowledgepage.RefTargetKnowledgePage).
const knowledgePageRefType = "knowledge_page"

// Planted is the plant's record: the platform id of every fixture page and
// the arm it was rendered for. It is written beside the run archive because
// every downstream reading (which page a fetch dereferenced, which page a
// search hit named) is a lookup through it, and because the arm is a property
// of what is actually in the store, so the run derives its arm from this
// record rather than trusting a flag to agree with it.
type Planted struct {
	PlantedAt time.Time         `json:"planted_at"`
	BaseURL   string            `json:"base_url"`
	Stripped  bool              `json:"stripped,omitempty"`
	Pages     map[string]string `json:"pages"` // fixture key -> platform page id
}

// Arm names the corpus arm this plant rendered.
func (p Planted) Arm() string {
	if p.Stripped {
		return "stripped"
	}
	return "graph"
}

// KeyByID inverts the map: platform page id to fixture key.
func (p Planted) KeyByID() map[string]string {
	out := make(map[string]string, len(p.Pages))
	for key, id := range p.Pages {
		out[id] = key
	}
	return out
}

// KeyForReference resolves an mcp:knowledge_page:<id> reference to its fixture
// key, reporting false for any other reference or an unknown id.
func (p Planted) KeyForReference(ref string) (string, bool) {
	const prefix = "mcp:knowledge_page:"
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		return "", false
	}
	key, ok := p.KeyByID()[ref[len(prefix):]]
	return key, ok
}

// Planter creates the fixture corpus on a running platform.
type Planter struct {
	baseURL string
	http    *http.Client
}

// NewPlanter returns a Planter writing to baseURL with an authenticated client.
func NewPlanter(baseURL string, httpClient *http.Client) *Planter {
	return &Planter{baseURL: baseURL, http: httpClient}
}

// pageRequest is the knowledge-page create payload.
type pageRequest struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Body     string   `json:"body"`
	Tags     []string `json:"tags"`
	ForceNew bool     `json:"force_new"`
}

// pageResponse is the subset of the created page the planter reads.
type pageResponse struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// refView mirrors the resolved reference the refs endpoint returns.
type refView struct {
	URN    string `json:"urn"`
	Type   string `json:"type"`
	Exists bool   `json:"exists"`
	Source string `json:"source"`
}

// refResponse is the page-references envelope.
type refResponse struct {
	Refs []refView `json:"refs"`
}

// Plant writes the whole corpus in the named arm and proves it landed.
//
// It refuses a store that already holds pages: a second corpus alongside the
// first would put two pages with the same content in front of the agent and
// nothing in the results would say which one an episode read. Pages are written
// in reference order (targets first) so each body can name real ids, and every
// page's declared references are read back before the plant is reported
// complete: in the graph arm an edge the platform did not record is an edge no
// episode can follow, and in the stripped arm a single recorded edge means the
// arm is not the arm and the run pair is uninterpretable.
//
// Pages whose references are already satisfied are written concurrently
// (grouped by reference depth), because a generated corpus at study scale is
// thousands of pages and a sequential plant would spend most of its wall
// clock waiting on round trips.
func (p *Planter) Plant(ctx context.Context, corpus graphfix.Corpus, stripped bool) (Planted, error) {
	if err := corpus.Validate(); err != nil {
		return Planted{}, err
	}
	existing, err := p.count(ctx)
	if err != nil {
		return Planted{}, err
	}
	if existing > 0 {
		return Planted{}, fmt.Errorf("graphprobe: the knowledge store already holds %d page(s); plant into an empty store so an episode cannot read a corpus from an earlier run", existing)
	}
	ids, err := p.createAll(ctx, corpus, stripped)
	planted := Planted{PlantedAt: time.Now().UTC(), BaseURL: p.baseURL, Stripped: stripped, Pages: ids}
	if err != nil {
		// The partial record still names every page that landed, so the
		// caller can archive it and Delete can clean the store; returning an
		// empty record here would orphan the pages behind the error.
		return planted, err
	}
	if err := p.verifyRefs(ctx, corpus, planted); err != nil {
		return planted, err
	}
	return planted, nil
}

// createAll writes every corpus page in reference order, one concurrent batch
// per reference depth: every page in a batch only references pages already
// created, so each body resolves to real ids at write time.
func (p *Planter) createAll(ctx context.Context, corpus graphfix.Corpus, stripped bool) (map[string]string, error) {
	levels, err := plantLevels(corpus)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(corpus.Pages))
	for _, level := range levels {
		if err := p.createLevel(ctx, corpus, stripped, level, ids); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// createLevel writes one reference-depth batch concurrently. Every body is
// resolved before any goroutine starts: a level's pages only reference lower
// levels, so the ids map is complete for them already, and resolving up
// front keeps the map free of concurrent reads while the batch writes into
// it.
func (p *Planter) createLevel(ctx context.Context, corpus graphfix.Corpus, stripped bool, level []string, ids map[string]string) error {
	requests := make([]pageRequest, 0, len(level))
	keys := make([]string, 0, len(level))
	for _, key := range level {
		page, ok := corpus.ByKey(key)
		if !ok {
			return fmt.Errorf("graphprobe: plant order names undefined page %q", key)
		}
		body, err := page.ResolveBody(ids, stripped)
		if err != nil {
			return err
		}
		requests = append(requests, pageRequest{
			Slug: page.Slug, Title: page.Title, Summary: page.Summary,
			Body: body, Tags: page.Tags, ForceNew: true,
		})
		keys = append(keys, page.Key)
	}
	var mu sync.Mutex
	grp := newWorkGroup(plantWorkers)
	for i := range requests {
		grp.Go(func() error {
			id, err := p.create(ctx, requests[i])
			if err != nil {
				return fmt.Errorf("graphprobe: creating %s: %w", keys[i], err)
			}
			mu.Lock()
			ids[keys[i]] = id
			mu.Unlock()
			return nil
		})
	}
	return grp.Wait()
}

// plantLevels groups the corpus plant order into reference-depth batches: a
// page lands in the batch after the deepest page it references, so batches
// can be written concurrently without ever writing a body before its targets.
func plantLevels(corpus graphfix.Corpus) ([][]string, error) {
	order, err := corpus.PlantOrder()
	if err != nil {
		return nil, err
	}
	depth := make(map[string]int, len(order))
	var levels [][]string
	for _, key := range order {
		page, _ := corpus.ByKey(key)
		d := 0
		for _, ref := range page.Refs() {
			if depth[ref] >= d {
				d = depth[ref] + 1
			}
		}
		depth[key] = d
		for len(levels) <= d {
			levels = append(levels, nil)
		}
		levels[d] = append(levels[d], key)
	}
	return levels, nil
}

// verifyRefs reads every page's declared references back and requires them to
// match the arm: exactly the fixture's edges in the graph arm, none at all in
// the stripped arm. This is the plant's completion condition; a probe that ran
// against a corpus with silent dead links (or a stripped corpus with live
// ones) would report numbers that belonged to the planter.
func (p *Planter) verifyRefs(ctx context.Context, corpus graphfix.Corpus, planted Planted) error {
	grp := newWorkGroup(plantWorkers)
	for _, page := range corpus.Sorted() {
		grp.Go(func() error { return p.verifyPageRefs(ctx, page, planted) })
	}
	return grp.Wait()
}

// verifyPageRefs checks one page's read-back references against its arm.
func (p *Planter) verifyPageRefs(ctx context.Context, page graphfix.Page, planted Planted) error {
	got, err := p.refs(ctx, planted.Pages[page.Key])
	if err != nil {
		return fmt.Errorf("graphprobe: reading references of %s: %w", page.Key, err)
	}
	gotKeys, err := declaredKeys(page.Key, got, planted)
	if err != nil {
		return err
	}
	if planted.Stripped {
		if len(gotKeys) != 0 {
			return fmt.Errorf("graphprobe: stripped plant left page %s with live references %v; the arm contrast is broken", page.Key, gotKeys)
		}
		return nil
	}
	want := page.Refs()
	sort.Strings(want)
	sort.Strings(gotKeys)
	if fmt.Sprint(want) != fmt.Sprint(gotKeys) {
		return fmt.Errorf("graphprobe: page %s declares references %v, fixture expects %v", page.Key, gotKeys, want)
	}
	return nil
}

// declaredKeys resolves one page's declared references to fixture keys,
// refusing anything that is not a live page of this corpus.
func declaredKeys(pageKey string, got []refView, planted Planted) ([]string, error) {
	out := make([]string, 0, len(got))
	for _, r := range got {
		switch {
		case r.Type != knowledgePageRefType:
			return nil, fmt.Errorf("graphprobe: page %s declares an unexpected reference of type %q", pageKey, r.Type)
		case !r.Exists:
			return nil, fmt.Errorf("graphprobe: page %s declares reference %s, which does not resolve to a live page", pageKey, r.URN)
		}
		key, ok := planted.KeyForReference(r.URN)
		if !ok {
			return nil, fmt.Errorf("graphprobe: page %s references unknown page %s", pageKey, r.URN)
		}
		out = append(out, key)
	}
	return out, nil
}

// count returns how many live knowledge pages the store holds.
func (p *Planter) count(ctx context.Context) (int, error) {
	var out struct {
		Total int `json:"total"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/v1/portal/knowledge-pages?limit=1", nil, &out); err != nil {
		return 0, fmt.Errorf("graphprobe: listing knowledge pages: %w", err)
	}
	return out.Total, nil
}

// create writes one page and returns its platform id.
func (p *Planter) create(ctx context.Context, req pageRequest) (string, error) {
	var out pageResponse
	if err := p.do(ctx, http.MethodPost, "/api/v1/portal/knowledge-pages", req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("create returned no id for slug %q", req.Slug)
	}
	return out.ID, nil
}

// refs reads one page's declared entity references.
func (p *Planter) refs(ctx context.Context, id string) ([]refView, error) {
	var out refResponse
	if err := p.do(ctx, http.MethodGet, "/api/v1/portal/knowledge-pages/"+id+"/refs", nil, &out); err != nil {
		return nil, err
	}
	return out.Refs, nil
}

// Delete removes every planted page, so a re-plant after a fixture edit does
// not need the database dropped. It is the operator's reset, not part of a run.
func (p *Planter) Delete(ctx context.Context, planted Planted) error {
	grp := newWorkGroup(plantWorkers)
	for key, id := range planted.Pages {
		grp.Go(func() error {
			if err := p.do(ctx, http.MethodDelete, "/api/v1/portal/knowledge-pages/"+id, nil, nil); err != nil {
				return fmt.Errorf("graphprobe: deleting %s: %w", key, err)
			}
			return nil
		})
	}
	return grp.Wait()
}

// do performs one REST call, decoding into out when it is non-nil.
func (p *Planter) do(ctx context.Context, method, path string, body, out any) error {
	req, err := p.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s %s: reading response: %w", method, path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s %s: status %d: %.300s", method, path, res.StatusCode, payload)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// request builds one authenticated REST request, JSON-encoding a body when there
// is one.
func (p *Planter) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
