package apigateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pagedUpstream is a test upstream serving pages of items in one of the
// pagination shapes the gateway detects. Each page carries perPage items
// numbered from the page's offset, and the page count is fixed, so the
// merged result a walk should produce is known exactly.
type pagedUpstream struct {
	t       *testing.T
	pages   int
	perPage int
	mode    string // cursor | link | odata | page | offset | none
	// hooks
	failPage    int   // page number that answers 500 (0 = none)
	rateLimit   int32 // pages that answer 429 with Retry-After before serving (decremented)
	retryAfter  string
	otherHost   string // link mode: page 1's next link points here
	deniedPath  bool   // link mode: page 1's next link points at /admin/secret
	hits        atomic.Int32
	inFlight    chan struct{} // when set, each page send is gated on a receive
	srv         *httptest.Server
	itemsKey    string
	envelopeKey string // when set, items are nested under envelopeKey.itemsKey
}

func (u *pagedUpstream) start() *pagedUpstream {
	u.t.Helper()
	if u.itemsKey == "" {
		u.itemsKey = "data"
	}
	u.srv = httptest.NewServer(http.HandlerFunc(u.serve))
	u.t.Cleanup(u.srv.Close)
	return u
}

func (u *pagedUpstream) pageOf(r *http.Request) int {
	q := r.URL.Query()
	switch u.mode {
	case "cursor":
		if c := q.Get("cursor"); c != "" {
			n, _ := strconv.Atoi(strings.TrimPrefix(c, "c"))
			return n
		}
	case "link", "odata", "page":
		if p := q.Get("page"); p != "" {
			n, _ := strconv.Atoi(p)
			return n
		}
	case "offset":
		if o := q.Get("offset"); o != "" {
			n, _ := strconv.Atoi(o)
			return n/u.perPage + 1
		}
	}
	return 1
}

func (u *pagedUpstream) serve(w http.ResponseWriter, r *http.Request) {
	u.hits.Add(1)
	if u.inFlight != nil {
		<-u.inFlight
	}
	page := u.pageOf(r)
	if page == u.failPage {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	if u.rateLimit > 0 && atomic.AddInt32(&u.rateLimit, -1) >= 0 {
		w.Header().Set("Retry-After", u.retryAfter)
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	items := make([]map[string]any, 0, u.perPage)
	if page <= u.pages {
		for i := range u.perPage {
			items = append(items, map[string]any{"id": (page-1)*u.perPage + i + 1, "n": "item"})
		}
	}
	body := map[string]any{}
	if u.envelopeKey != "" {
		body[u.envelopeKey] = map[string]any{u.itemsKey: items}
	} else {
		body[u.itemsKey] = items
	}
	if page < u.pages {
		u.signal(w, r, body, page+1)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (u *pagedUpstream) signal(w http.ResponseWriter, r *http.Request, body map[string]any, next int) {
	switch u.mode {
	case "cursor":
		body["next_cursor"] = fmt.Sprintf("c%d", next)
	case "link":
		target := u.srv.URL + r.URL.Path + "?page=" + strconv.Itoa(next)
		if u.otherHost != "" && next == 2 {
			target = u.otherHost + r.URL.Path + "?page=2"
		}
		if u.deniedPath && next == 2 {
			target = u.srv.URL + "/admin/secret?page=2"
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, target))
	case "odata":
		body["@odata.nextLink"] = u.srv.URL + r.URL.Path + "?page=" + strconv.Itoa(next)
	}
}

// walkExportToolkit wires an export toolkit against the upstream with the
// fakes the caller inspects.
func walkExportToolkit(t *testing.T, up *pagedUpstream, store *fakeExportAssetStore, s3 *fakeExportS3Client) *Toolkit {
	t.Helper()
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, s3)
	return buildExportTestToolkit(t, up.srv.URL, &deps)
}

func exportWalk(t *testing.T, tk *Toolkit, in exportInput) (*mcp.CallToolResult, *exportOutput) {
	t.Helper()
	res, payload, err := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, in)
	if err != nil {
		t.Fatalf("handleExport: %v", err)
	}
	out, _ := payload.(*exportOutput)
	return res, out
}

// renderedText is the JSON text of a successful result, which the
// error-asserting resultText helper refuses.
func renderedText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T", res.Content[0])
	}
	return tc.Text
}

func decodeMergedIDs(t *testing.T, data []byte) []int {
	t.Helper()
	var items []struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("asset content is not a JSON array: %v\n%s", err, truncateForLog(data))
	}
	ids := make([]int, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func truncateForLog(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}
	return string(b)
}

func assertSequence(t *testing.T, ids []int, want int) {
	t.Helper()
	if len(ids) != want {
		t.Fatalf("merged %d items; want %d", len(ids), want)
	}
	for i, id := range ids {
		if id != i+1 {
			t.Fatalf("item %d has id %d; pages merged out of order", i, id)
		}
	}
}

// TestExportWalk_EveryShapeProducesTheSameAsset is the acceptance case:
// 160 pages of 100 items in each pagination shape produce one asset whose
// content is the 16,000-item array, and the output reports the walk.
func TestExportWalk_EveryShapeProducesTheSameAsset(t *testing.T) {
	// A page-numbered walk has no signal to end on; it ends on the first
	// empty page, which is a page fetched, so those two shapes report one
	// more page for the same 16,000 items.
	cases := []struct {
		mode      string
		query     map[string]any
		pag       PaginateInput
		wantPages int
	}{
		{"cursor", map[string]any{"per_page": 100}, PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 500}, 160},
		{"link", nil, PaginateInput{Items: "data", MaxPages: 500}, 160},
		{"odata", nil, PaginateInput{Items: "data", MaxPages: 500}, 160},
		{"page", map[string]any{"page": 1}, PaginateInput{Items: "data", PageParam: "page", MaxPages: 500}, 161},
		{"offset", map[string]any{"offset": 0}, PaginateInput{Items: "data", PageParam: "offset", PageStep: 100, MaxPages: 500}, 161},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			up := (&pagedUpstream{t: t, pages: 160, perPage: 100, mode: tc.mode}).start()
			store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
			tk := walkExportToolkit(t, up, store, s3)
			pag := tc.pag
			res, out := exportWalk(t, tk, exportInput{
				Connection: "crm", Method: "GET", Path: "/v1/changelog", Query: tc.query,
				Name: "changelog.json", Paginate: &pag,
			})
			if res.IsError {
				t.Fatalf("export failed: %s", resultText(t, res))
			}
			if out.PagesFetched != tc.wantPages || out.ItemsMerged != 16000 || out.StoppedBy != "end" {
				t.Fatalf("walk stats = %+v; want %d pages, 16000 items, end", *out.WalkStats, tc.wantPages)
			}
			if out.ContentType != "application/json" {
				t.Errorf("content type %q; want application/json", out.ContentType)
			}
			if len(s3.puts) != 1 {
				t.Fatalf("S3 puts = %d; want 1", len(s3.puts))
			}
			assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 16000)
			if len(store.inserted) != 1 {
				t.Fatalf("asset rows = %d; want 1", len(store.inserted))
			}
			params := store.inserted[0].Provenance.ToolCalls[0].Parameters
			if params["pages_fetched"] != tc.wantPages || params["stopped_by"] != "end" {
				t.Errorf("provenance = %v; want pages_fetched %d, stopped_by end", params, tc.wantPages)
			}
			if params["final_cursor"] == "" {
				t.Errorf("provenance final_cursor is empty on a 160-page walk")
			}
			// The one call's audit row carries the walk.
			facts, _ := res.Meta["audit_result"].(map[string]any)
			if facts["pages_fetched"] != tc.wantPages || facts["stopped_by"] != "end" {
				t.Errorf("audit_result meta = %v", facts)
			}
			// The hit count proves one request per page, none re-issued.
			if got := up.hits.Load(); int(got) != tc.wantPages {
				t.Errorf("upstream hits = %d; want %d", got, tc.wantPages)
			}
		})
	}
}

// TestExportWalk_NestedItemsAndRootArray covers the two Items forms
// beyond a top-level key: a dotted path and "$".
func TestExportWalk_NestedItemsAndRootArray(t *testing.T) {
	t.Run("dotted path", func(t *testing.T) {
		up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "cursor", envelopeKey: "result", itemsKey: "items"}).start()
		s3 := &fakeExportS3Client{}
		tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, s3)
		res, out := exportWalk(t, tk, exportInput{
			Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
			Paginate: &PaginateInput{Items: "result.items", CursorParam: "cursor"},
		})
		if res.IsError {
			t.Fatalf("export failed: %s", resultText(t, res))
		}
		if out.ItemsMerged != 6 {
			t.Errorf("items_merged = %d; want 6", out.ItemsMerged)
		}
		assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 6)
	})
	t.Run("root array", func(t *testing.T) {
		page := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			page++
			w.Header().Set("Content-Type", "application/json")
			if page < 3 {
				w.Header().Set("Link", fmt.Sprintf(`<http://%s/v1/x?page=%d>; rel="next"`, r.Host, page+1))
			}
			_, _ = fmt.Fprintf(w, `[{"id":%d},{"id":%d}]`, page*2-1, page*2)
		}))
		t.Cleanup(srv.Close)
		s3 := &fakeExportS3Client{}
		deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, s3)
		tk := buildExportTestToolkit(t, srv.URL, &deps)
		res, out := exportWalk(t, tk, exportInput{
			Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
			Paginate: &PaginateInput{Items: "$"},
		})
		if res.IsError {
			t.Fatalf("export failed: %s", resultText(t, res))
		}
		if out.PagesFetched != 3 {
			t.Errorf("pages_fetched = %d; want 3", out.PagesFetched)
		}
		assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 6)
	})
}

// TestExportWalk_ItemsPreservedVerbatim proves items are copied as the
// upstream sent them: a 19-digit id survives, and key order is kept.
func TestExportWalk_ItemsPreservedVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"z":1,"id":9007199254740993,"a":"<b>"}]}`)
	}))
	t.Cleanup(srv.Close)
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, s3)
	tk := buildExportTestToolkit(t, srv.URL, &deps)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data"},
	})
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	want := `[{"z":1,"id":9007199254740993,"a":"<b>"}]`
	if got := string(s3.puts[0].Data); got != want {
		t.Errorf("asset content = %s; want %s", got, want)
	}
}

// TestExportWalk_NextLinkPinnedToHost: a next link pointing at another
// host fails the call before any request reaches that host.
func TestExportWalk_NextLinkPinnedToHost(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "link", otherHost: other.URL}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data"},
	})
	if !res.IsError {
		t.Fatalf("expected the walk to refuse the foreign next link")
	}
	text := resultText(t, res)
	if !strings.Contains(text, "page 1") || !strings.Contains(text, "pinned to") {
		t.Errorf("error = %q; want the page number and the pin", text)
	}
	if otherHits.Load() != 0 {
		t.Errorf("the other host was requested %d time(s); want 0", otherHits.Load())
	}
	if len(s3.puts) != 0 || len(store.inserted) != 0 {
		t.Errorf("a failed walk wrote an asset: puts=%d rows=%d", len(s3.puts), len(store.inserted))
	}
}

// denyAdminPolicy refuses any path under /admin.
type denyAdminPolicy struct {
	calls atomic.Int32
}

func (p *denyAdminPolicy) Allow(_ context.Context, _, _, path, _ string) (allowed bool, reason string) {
	p.calls.Add(1)
	if strings.HasPrefix(path, "/admin/") {
		return false, "persona denies /admin/*"
	}
	return true, ""
}

// TestExportWalk_NextLinkCheckedAgainstRoutePolicy: a next link onto a
// path the persona's policy denies fails the call in the policy's words,
// and the policy is consulted for every page.
func TestExportWalk_NextLinkCheckedAgainstRoutePolicy(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "link", deniedPath: true}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)
	policy := &denyAdminPolicy{}
	tk.SetRoutePolicy(policy)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data"},
	})
	if !res.IsError {
		t.Fatalf("expected the policy to refuse page 2")
	}
	if text := resultText(t, res); !strings.Contains(text, "persona denies /admin/*") || !strings.Contains(text, "page 2") {
		t.Errorf("error = %q; want the policy's refusal naming page 2", text)
	}
	if up.hits.Load() != 1 {
		t.Errorf("upstream hits = %d; the denied page must not be requested", up.hits.Load())
	}
	// handleExport's own check plus one per page walked (pages 1 and 2).
	if policy.calls.Load() != 3 {
		t.Errorf("policy consulted %d times; want 3", policy.calls.Load())
	}
	if len(store.inserted) != 0 {
		t.Errorf("a refused walk wrote an asset row")
	}
}

// TestExportWalk_RetryAfterPausesAndResumes: a 429 with Retry-After is
// retried after the interval and the walk completes.
func TestExportWalk_RetryAfterPausesAndResumes(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "cursor", rateLimit: 1, retryAfter: "1"}).start()
	s3 := &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, s3)
	start := time.Now()
	res, out := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("walk finished in %s; the Retry-After pause was not honored", elapsed)
	}
	if out.PagesFetched != 3 || out.ItemsMerged != 6 {
		t.Errorf("stats = %+v; want 3 pages, 6 items", *out.WalkStats)
	}
	if up.hits.Load() != 4 {
		t.Errorf("upstream hits = %d; want 4 (one refused, three served)", up.hits.Load())
	}
	assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 6)
}

// TestExportWalk_RetryAfterPastTimeoutFails: a pause the call's timeout
// cannot contain fails immediately, naming the interval.
func TestExportWalk_RetryAfterPastTimeoutFails(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "cursor", rateLimit: 1, retryAfter: "3600"}).start()
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, &fakeExportS3Client{})
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x", TimeoutSeconds: 2,
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if !res.IsError {
		t.Fatalf("expected failure")
	}
	if text := resultText(t, res); !strings.Contains(text, "retry after 1h0m0s") {
		t.Errorf("error = %q; want the interval named", text)
	}
}

// TestExportWalk_RetryAfterRepeatedPastTheBoundFails: an upstream that
// refuses the same page with a zero interval on every request is not
// polled until the timeout; the page fails after the bound.
func TestExportWalk_RetryAfterRepeatedPastTheBoundFails(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "cursor", rateLimit: 100, retryAfter: "0"}).start()
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, &fakeExportS3Client{})
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, "page 1: upstream returned 429 with Retry-After 11 times in a row") {
		t.Errorf("error = %q", text)
	}
	if up.hits.Load() != 11 {
		t.Errorf("upstream hits = %d; want the bound plus the first request", up.hits.Load())
	}
}

// TestExportWalk_PageFailureNamesThePageAndWritesNothing: a 500 on page 2
// fails the call naming the page, and no asset exists.
func TestExportWalk_PageFailureNamesThePageAndWritesNothing(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "cursor", failPage: 2}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if !res.IsError {
		t.Fatalf("expected failure")
	}
	if text := resultText(t, res); !strings.Contains(text, "page 2: upstream returned 500") {
		t.Errorf("error = %q", text)
	}
	if len(s3.puts) != 0 || len(store.inserted) != 0 {
		t.Errorf("failed walk left an asset: puts=%d rows=%d", len(s3.puts), len(store.inserted))
	}
}

// TestExportWalk_MaxPagesReported: reaching max_pages returns the pages
// fetched with stopped_by max_pages.
func TestExportWalk_MaxPagesReported(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 10, perPage: 2, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)
	res, out := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 4},
	})
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	if out.PagesFetched != 4 || out.ItemsMerged != 8 || out.StoppedBy != "max_pages" {
		t.Errorf("stats = %+v", *out.WalkStats)
	}
	if up.hits.Load() != 4 {
		t.Errorf("upstream hits = %d; want 4", up.hits.Load())
	}
	assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 8)
	if got := store.inserted[0].Provenance.ToolCalls[0].Parameters["final_cursor"]; got != "c4" {
		t.Errorf("final_cursor = %v; want c4", got)
	}
}

// TestExportWalk_OverCapFailsAllOrNothing: a walk whose merged output
// passes platform.export.max_bytes fails and leaves no asset.
func TestExportWalk_OverCapFailsAllOrNothing(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 50, perPage: 10, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, s3)
	deps.Config.MaxBytes = 2048
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if !res.IsError {
		t.Fatalf("expected the cap to fail the walk")
	}
	if text := resultText(t, res); !strings.Contains(text, "exceeded api_export cap of 2048 bytes") {
		t.Errorf("error = %q", text)
	}
	if len(s3.puts) != 0 || len(store.inserted) != 0 {
		t.Errorf("over-cap walk left an asset: puts=%d rows=%d", len(s3.puts), len(store.inserted))
	}
	if up.hits.Load() >= 50 {
		t.Errorf("upstream hits = %d; the walk kept fetching past the cap", up.hits.Load())
	}
}

// slowS3 reads the streamed body one page at a time on the test's
// signal, so the test can observe how far ahead the walk runs.
type slowS3 struct {
	release chan struct{}
	read    atomic.Int64
	done    chan struct{}
}

func (s *slowS3) PutObjectStream(_ context.Context, _, _ string, body io.Reader, _ string) (int64, error) {
	defer close(s.done)
	buf := make([]byte, 64*1024)
	for {
		<-s.release
		n, err := body.Read(buf)
		s.read.Add(int64(n))
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.read.Load(), nil
			}
			return s.read.Load(), err
		}
	}
}

// TestExportWalk_MemoryBoundedByBackpressure proves the walk holds one
// page, not the merged result: with storage not reading, the upstream
// is asked for no more than the page in flight plus the one the pipe is
// blocked on, however many pages remain.
func TestExportWalk_MemoryBoundedByBackpressure(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 160, perPage: 100, mode: "cursor"}).start()
	s3 := &slowS3{release: make(chan struct{}), done: make(chan struct{})}
	store := &fakeExportAssetStore{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.S3Client = s3
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)

	var wg sync.WaitGroup
	wg.Add(1)
	var res *mcp.CallToolResult
	go func() {
		defer wg.Done()
		res, _ = exportWalk(t, tk, exportInput{
			Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
			Paginate: &PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 500},
		})
	}()
	// Let the walk fetch and block on the unread pipe, then look.
	time.Sleep(300 * time.Millisecond)
	if hits := up.hits.Load(); hits > 2 {
		t.Errorf("upstream served %d pages while storage read nothing; the walk is buffering ahead", hits)
	}
	// Drain: release reads until the put completes.
	go func() {
		for {
			select {
			case s3.release <- struct{}{}:
			case <-s3.done:
				return
			}
		}
	}()
	wg.Wait()
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	if up.hits.Load() != 160 {
		t.Errorf("upstream hits = %d; want 160", up.hits.Load())
	}
}

// TestExportWalk_ValidationErrors: the paginate block is checked before
// any request is sent.
func TestExportWalk_ValidationErrors(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 2, perPage: 2, mode: "cursor"}).start()
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, &fakeExportS3Client{})
	cases := []struct {
		name string
		in   exportInput
		want string
	}{
		{"items missing", exportInput{Paginate: &PaginateInput{}}, "paginate.items is required"},
		{"page_param without start", exportInput{Paginate: &PaginateInput{Items: "data", PageParam: "page"}}, "query_params does not carry it"},
		{"page_param not integer", exportInput{Query: map[string]any{"page": "x"}, Paginate: &PaginateInput{Items: "data", PageParam: "page"}}, "is not an integer"},
		{"negative max_pages", exportInput{Paginate: &PaginateInput{Items: "data", MaxPages: -1}}, "must be positive"},
		{"cursor without cursor_param", exportInput{Paginate: &PaginateInput{Items: "data"}}, "names no cursor_param"},
		{"items not an array", exportInput{Paginate: &PaginateInput{Items: "$"}}, "is not a JSON array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Connection, in.Method, in.Path, in.Name = "crm", "GET", "/v1/x", "x"
			res, _ := exportWalk(t, tk, in)
			if !res.IsError {
				t.Fatalf("expected an error result")
			}
			if text := resultText(t, res); !strings.Contains(text, tc.want) {
				t.Errorf("error = %q; want %q", text, tc.want)
			}
		})
	}
}

// --- api_invoke_endpoint -------------------------------------------------

func walkInvokeToolkit(t *testing.T, up *pagedUpstream, withExport bool) *Toolkit {
	t.Helper()
	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": up.srv.URL, "max_response_bytes": 4096}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	if withExport {
		deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
		tk.SetExportDeps(deps)
	}
	return tk
}

func invokeWalkCall(t *testing.T, tk *Toolkit, in InvokeInput) (*mcp.CallToolResult, InvokeOutput) {
	t.Helper()
	res, payload, err := tk.handleInvoke(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	out, _ := payload.(InvokeOutput)
	return res, out
}

// TestInvokeWalk_MergesInline: the same paginate block on
// api_invoke_endpoint returns the merged array inline with the walk stats,
// and the audit meta carries them.
func TestInvokeWalk_MergesInline(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 5, perPage: 3, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, true)
	res, out := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if res.IsError {
		t.Fatalf("invoke failed: %s", resultText(t, res))
	}
	if out.PagesFetched != 5 || out.ItemsMerged != 15 || out.StoppedBy != "end" {
		t.Fatalf("stats = %+v", *out.WalkStats)
	}
	if out.Status != 200 || out.Pagination != nil || out.BodyTruncated {
		t.Errorf("out = %+v; want status 200, no pagination, not truncated", out)
	}
	data, err := json.Marshal(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	assertSequence(t, decodeMergedIDs(t, data), 15)
	// The rendered JSON carries the stats flat on the output.
	text := renderedText(t, res)
	if !strings.Contains(text, `"pages_fetched": 5`) || !strings.Contains(text, `"stopped_by": "end"`) {
		t.Errorf("rendered output lacks the walk stats: %s", text)
	}
	facts, _ := res.Meta["audit_result"].(map[string]any)
	if facts["pages_fetched"] != 5 {
		t.Errorf("audit_result meta = %v", facts)
	}
}

// TestInvokeWalk_OverCapStopsAndSteersToExport: past max_response_bytes the
// inline walk returns the pages that fit, flags truncation, steers to
// api_export, and hands back where to resume.
func TestInvokeWalk_OverCapStopsAndSteersToExport(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 50, perPage: 20, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, true)
	res, out := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if res.IsError {
		t.Fatalf("invoke failed: %s", resultText(t, res))
	}
	if out.StoppedBy != "max_bytes" || !out.BodyTruncated {
		t.Fatalf("out = %+v; want stopped_by max_bytes and body_truncated", *out.WalkStats)
	}
	if out.PagesFetched == 0 || out.PagesFetched >= 50 {
		t.Errorf("pages_fetched = %d; want the pages that fit under 4096 bytes", out.PagesFetched)
	}
	if !strings.Contains(out.Hint, "api_export") {
		t.Errorf("hint = %q; want a steer to api_export", out.Hint)
	}
	wantCursor := fmt.Sprintf("c%d", out.PagesFetched+1)
	if out.Pagination == nil || out.Pagination.NextCursor != wantCursor {
		t.Errorf("pagination = %+v; want next_cursor %s to resume from", out.Pagination, wantCursor)
	}
	data, _ := json.Marshal(out.Body)
	assertSequence(t, decodeMergedIDs(t, data), out.ItemsMerged)

	// Without api_export on the deployment the hint is cleared.
	tk2 := walkInvokeToolkit(t, up, false)
	_, out2 := invokeWalkCall(t, tk2, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if out2.Hint != "" {
		t.Errorf("hint = %q with no api_export registered; want empty", out2.Hint)
	}
}

// TestInvokeWalk_MaxPagesHandsBackResume: page numbering stops at
// max_pages and reports the next page number.
func TestInvokeWalk_MaxPagesHandsBackResume(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 10, perPage: 1, mode: "page"}).start()
	tk := walkInvokeToolkit(t, up, false)
	_, out := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Query: map[string]any{"page": 1},
		Paginate: &PaginateInput{Items: "data", PageParam: "page", MaxPages: 3},
	})
	if out.StoppedBy != "max_pages" || out.PagesFetched != 3 {
		t.Fatalf("stats = %+v", out.WalkStats)
	}
	if out.Pagination == nil || out.Pagination.NextCursor != "4" || out.Pagination.Source != "page_param" {
		t.Errorf("pagination = %+v; want page 4", out.Pagination)
	}
}

// TestInvokeWalk_OperationIDAndResolvedPath: operation_id addressing works
// for a walk and the resolved path is reported.
func TestInvokeWalk_FailedPageIsToolError(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 1, mode: "cursor", failPage: 3}).start()
	tk := walkInvokeToolkit(t, up, false)
	res, _ := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if !res.IsError {
		t.Fatalf("expected a tool error")
	}
	if text := resultText(t, res); !strings.Contains(text, "page 3: upstream returned 500") {
		t.Errorf("error = %q", text)
	}
}

// TestInvokeWalk_RefusedOnRawPassthrough: the raw REST route streams one
// body and cannot merge pages.
func TestInvokeWalk_RefusedOnRawPassthrough(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 1, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, false)
	ctx := WithRawPassthrough(context.Background(), &RawPassthrough{Sink: &discardSink{}})
	res, _, _ := tk.handleInvoke(ctx, nil, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if !res.IsError || !strings.Contains(resultText(t, res), "raw passthrough") {
		t.Errorf("result = %s; want a refusal naming the raw route", resultText(t, res))
	}
	if up.hits.Load() != 0 {
		t.Errorf("upstream hit %d times; the refusal must precede any request", up.hits.Load())
	}
}

type discardSink struct{}

func (*discardSink) AddHeader(_, _ string)       {}
func (*discardSink) SetStatus(int)               {}
func (*discardSink) Write(p []byte) (int, error) { return len(p), nil }

// TestInvoke_WithoutPaginateIsUnchanged: with no block, the signal is
// reported and not followed, and the output carries no walk fields.
func TestInvoke_WithoutPaginateIsUnchanged(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 1, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, false)
	res, out := invokeWalkCall(t, tk, InvokeInput{Connection: "crm", Method: "GET", Path: "/v1/x"})
	if out.Pagination == nil || out.Pagination.NextCursor != "c2" {
		t.Errorf("pagination = %+v; want next_cursor c2 reported", out.Pagination)
	}
	if out.WalkStats != nil || strings.Contains(renderedText(t, res), "pages_fetched") {
		t.Errorf("single-page output carries walk fields: %s", renderedText(t, res))
	}
	if up.hits.Load() != 1 {
		t.Errorf("upstream hits = %d; the signal must not be followed", up.hits.Load())
	}
	if _, ok := res.Meta["audit_result"]; ok {
		t.Errorf("single-page result stamped audit_result")
	}
}

// --- util fetch_url --------------------------------------------------------

// utilRelayToolkit builds a toolkit whose handler=internal "util"
// connection stands in for fetch_url: it reads the url from the body and
// relays that document, recording every url it fetched.
func utilRelayToolkit(t *testing.T) (tk *Toolkit, fetched *[]string) {
	t.Helper()
	fetched = &[]string{}
	internal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		*fetched = append(*fetched, in.URL)
		resp, err := http.Get(in.URL) //nolint:gosec,noctx // test relay of a test URL
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
	tk = New("primary")
	tk.SetInternalHandler(internal)
	if err := tk.AddConnection("util", map[string]any{"handler": "internal"}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	return tk, fetched
}

// assertUtilWalkedThreePages checks a fetch_url walk over a 3-page, 2-per-page
// document fetched every page by advancing the url in the body.
func assertUtilWalkedThreePages(t *testing.T, out InvokeOutput, fetched []string) {
	t.Helper()
	if out.WalkStats == nil || out.PagesFetched != 3 || out.ItemsMerged != 6 {
		t.Fatalf("stats = %+v", out.WalkStats)
	}
	if len(fetched) != 3 || !strings.HasSuffix(fetched[1], "/v1/x?page=2") {
		t.Errorf("fetched = %v; want the body url advanced per page", fetched)
	}
}

// TestWalk_UtilFetchAddressIsTheBodyURL: on a handler=internal connection
// whose body carries a url, the walk moves that url, and a next link is
// pinned to the fetched host rather than the connection's.
func TestWalk_UtilFetchAddressIsTheBodyURL(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "link"}).start()
	tk, fetched := utilRelayToolkit(t)
	_, out := invokeWalkCall(t, tk, InvokeInput{
		Connection: "util", Method: "POST", Path: "/util/fetch",
		Body:     map[string]any{"url": up.srv.URL + "/v1/x"},
		Paginate: &PaginateInput{Items: "data"},
	})
	assertUtilWalkedThreePages(t, out, *fetched)
}

// TestWalk_UtilFetchBodyAsJSONString: a fetch_url body sent as a string of
// JSON, the form a single fetch already accepts, walks the url it names.
func TestWalk_UtilFetchBodyAsJSONString(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 2, mode: "link"}).start()
	tk, fetched := utilRelayToolkit(t)
	_, out := invokeWalkCall(t, tk, InvokeInput{
		Connection: "util", Method: "POST", Path: "/util/fetch",
		Body:     `{"url": "` + up.srv.URL + `/v1/x"}`,
		Paginate: &PaginateInput{Items: "data"},
	})
	assertUtilWalkedThreePages(t, out, *fetched)
}

// --- failure corners the shape tests do not reach ---------------------------

func TestNewPageWalk_Refusals(t *testing.T) {
	if _, err := newPageWalk(invocation{cfg: Config{BaseURL: "https://api.example.com"}}, InvokeInput{}, nil, nil); err == nil {
		t.Error("nil paginate accepted")
	}
	if _, err := newPageWalk(invocation{cfg: Config{BaseURL: "nope"}}, InvokeInput{Paginate: &PaginateInput{Items: "data"}}, nil, nil); err == nil {
		t.Error("bad base_url accepted")
	}
}

// TestWalk_RequestBuildAndTransportFailuresNameThePage: a page whose
// request cannot be built (a reserved header) or sent (upstream gone)
// fails the call at that page.
func TestWalk_RequestBuildAndTransportFailuresNameThePage(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 1, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, false)
	res, _ := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Headers: map[string]string{"Authorization": "x"},
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, "page 1") || !strings.Contains(text, "Authorization header is reserved") {
		t.Errorf("error = %q", text)
	}
	up.srv.Close()
	res, _ = invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, "page 1") || !strings.Contains(text, "connection refused") {
		t.Errorf("error = %q", text)
	}
}

// TestWalk_PageReadLimits: a page over max_response_bytes fails the page
// (the walk cannot truncate a page and stay a collection), and an
// exhausted in-flight budget keeps its structured 429 shape.
func TestWalk_PageReadLimits(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 300, mode: "cursor"}).start()
	tk := walkInvokeToolkit(t, up, false)
	res, _ := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, "page 1: page exceeds the connection's max_response_bytes (4096)") {
		t.Errorf("error = %q", text)
	}

	tk.SetMemBudget(NewMemBudget(16))
	res, _ = invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, ErrCodeBudgetExhausted) {
		t.Errorf("error = %q; want the structured budget refusal", text)
	}
}

// TestWalk_NonJSONPageFails: a walk needs a JSON page to find its items
// in; a text page fails at that page.
func TestWalk_NonJSONPageFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "not json")
	}))
	t.Cleanup(srv.Close)
	tk := New("primary")
	if err := tk.AddConnection("crm", map[string]any{"base_url": srv.URL}); err != nil {
		t.Fatal(err)
	}
	res, _ := invokeWalkCall(t, tk, InvokeInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Paginate: &PaginateInput{Items: "data"},
	})
	if text := resultText(t, res); !strings.Contains(text, "page 1") || !strings.Contains(text, "not a JSON object") {
		t.Errorf("error = %q", text)
	}
}

// TestExportWalk_StorageFailureIsReported: when storage refuses the
// stream, the walk stops and the storage error is what the caller sees.
func TestExportWalk_StorageFailureIsReported(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 1, mode: "cursor"}).start()
	s3 := &fakeExportS3Client{putErr: errors.New("bucket unavailable")}
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, s3)
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/x", Name: "x",
		Paginate: &PaginateInput{Items: "data", CursorParam: "cursor"},
	})
	if text := resultText(t, res); !strings.Contains(text, "bucket unavailable") {
		t.Errorf("error = %q", text)
	}
}

func TestJSONArrayWriter(t *testing.T) {
	var sb strings.Builder
	a := &jsonArrayWriter{w: &sb}
	if err := a.write([]json.RawMessage{json.RawMessage("1"), json.RawMessage(`{"a":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := a.write([]json.RawMessage{json.RawMessage("3")}); err != nil {
		t.Fatal(err)
	}
	if err := a.close(); err != nil || sb.String() != `[1,{"a":2},3]` {
		t.Errorf("got %q err %v", sb.String(), err)
	}
	var empty strings.Builder
	if err := (&jsonArrayWriter{w: &empty}).close(); err != nil || empty.String() != "[]" {
		t.Errorf("empty walk = %q err %v", empty.String(), err)
	}
	pr, pw := io.Pipe()
	_ = pr.Close()
	b := &jsonArrayWriter{w: pw}
	if err := b.write([]json.RawMessage{json.RawMessage("1")}); !errors.Is(err, errWalkConsumerStopped) {
		t.Errorf("closed-pipe write classified as %v", err)
	}
	if err := (&jsonArrayWriter{w: pw}).close(); !errors.Is(err, errWalkConsumerStopped) {
		t.Errorf("closed-pipe close classified as %v", err)
	}
	if err := consumerError(errors.New("disk full")); err == nil || errors.Is(err, errWalkConsumerStopped) {
		t.Errorf("a storage error classified as the consumer stopping: %v", err)
	}
}
