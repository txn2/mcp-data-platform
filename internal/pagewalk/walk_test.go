package pagewalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeRequester serves pages from memory, keyed by the cursor on the
// target, and records what it was asked for.
type fakeRequester struct {
	pages    int
	perPage  int
	targets  []Target
	refusals int    // 429s to answer before serving
	refuseAs string // Retry-After value on a refusal
	failPage int
	doErr    error
	readErr  error
}

func (f *fakeRequester) Do(_ context.Context, t Target) (*http.Response, error) {
	f.targets = append(f.targets, t)
	if f.doErr != nil {
		return nil, f.doErr
	}
	page := 1
	if c, ok := t.Query["cursor"].(string); ok {
		page, _ = strconv.Atoi(c)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
	if f.refusals > 0 {
		f.refusals--
		resp.StatusCode = http.StatusTooManyRequests
		resp.Header.Set("Retry-After", f.refuseAs)
		return resp, nil
	}
	if page == f.failPage {
		resp.StatusCode = http.StatusInternalServerError
		return resp, nil
	}
	items := make([]string, 0, f.perPage)
	if page <= f.pages {
		for i := range f.perPage {
			items = append(items, strconv.Itoa((page-1)*f.perPage+i+1))
		}
	}
	body := fmt.Sprintf(`{"data":[%s]`, strings.Join(items, ","))
	if page < f.pages {
		body += fmt.Sprintf(`,"next_cursor":"%d"`, page+1)
	}
	body += "}"
	resp.Body = io.NopCloser(strings.NewReader(body))
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func (f *fakeRequester) ReadPage(resp *http.Response) (Page, error) {
	defer func() { _ = resp.Body.Close() }()
	if f.readErr != nil {
		return Page{}, f.readErr
	}
	if resp.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	return Page{Status: resp.StatusCode, Header: resp.Header, ContentType: resp.Header.Get("Content-Type"), Body: body}, nil
}

func newWalk(t *testing.T, req *fakeRequester, p PaginateInput, sink Sink) *Walk {
	t.Helper()
	w, err := New(Options{
		Paginate:  p,
		Address:   AddressSpec{BaseURL: "https://api.example.com", Path: "/v1/items"},
		Requester: req, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWalk_MergesEveryPage(t *testing.T) {
	req := &fakeRequester{pages: 4, perPage: 3}
	merge := &InlineMerge{}
	w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, merge.Add)
	if err := w.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.Stats != (WalkStats{PagesFetched: 4, ItemsMerged: 12, StoppedBy: StoppedByEnd}) {
		t.Errorf("stats = %+v", w.Stats)
	}
	if w.Resume != nil || w.Lead == nil || w.Lead.NextCursor != "4" || w.Last.Status != 200 {
		t.Errorf("resume %+v lead %+v last %+v", w.Resume, w.Lead, w.Last)
	}
	if got, _ := json.Marshal(merge.Merged()); string(got) != "[1,2,3,4,5,6,7,8,9,10,11,12]" {
		t.Errorf("merged = %s", got)
	}
	if len(req.targets) != 4 || req.targets[3].Query["cursor"] != "4" {
		t.Errorf("targets = %v", req.targets)
	}
}

func TestWalk_AuthorizeRunsOnEveryPage(t *testing.T) {
	req := &fakeRequester{pages: 3, perPage: 1}
	var seen []string
	w, err := New(Options{
		Paginate:  PaginateInput{Items: "data", CursorParam: "cursor"},
		Address:   AddressSpec{BaseURL: "https://api.example.com", Path: "/v1/items"},
		Requester: req,
		Authorize: func(t Target) error {
			seen = append(seen, fmt.Sprint(t.Query["cursor"]))
			if len(seen) == 3 {
				return errors.New("persona denies page 3")
			}
			return nil
		},
		Sink: (&InlineMerge{}).Add,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = w.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "page 3: persona denies page 3") {
		t.Errorf("err = %v", err)
	}
	if len(req.targets) != 2 {
		t.Errorf("the refused page was requested: %d requests", len(req.targets))
	}
}

func TestWalk_StopsAtBounds(t *testing.T) {
	t.Run("max_pages", func(t *testing.T) {
		req := &fakeRequester{pages: 9, perPage: 1}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 2}, (&InlineMerge{}).Add)
		if err := w.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w.Stats.StoppedBy != StoppedByMaxPages || w.Stats.PagesFetched != 2 || w.Resume == nil || w.Resume.NextCursor != "3" {
			t.Errorf("stats %+v resume %+v", w.Stats, w.Resume)
		}
		if w.Lead == nil || w.Lead.NextCursor != "2" {
			t.Errorf("lead = %+v; want the cursor that addressed page 2", w.Lead)
		}
	})
	t.Run("max_bytes", func(t *testing.T) {
		req := &fakeRequester{pages: 9, perPage: 2}
		merge := &InlineMerge{Limit: 9}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, merge.Add)
		if err := w.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w.Stats.StoppedBy != StoppedByMaxBytes || w.Stats.PagesFetched != 2 || w.Resume == nil || w.Resume.NextCursor != "3" {
			t.Errorf("stats %+v resume %+v", w.Stats, w.Resume)
		}
		if len(merge.Merged()) != 4 {
			t.Errorf("merged %d items; want the two pages that fit", len(merge.Merged()))
		}
	})
	t.Run("empty page ends", func(t *testing.T) {
		req := &fakeRequester{pages: 0, perPage: 1}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, (&InlineMerge{}).Add)
		if err := w.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w.Stats != (WalkStats{PagesFetched: 1, StoppedBy: StoppedByEnd}) {
			t.Errorf("stats = %+v", w.Stats)
		}
	})
}

func TestWalk_Failures(t *testing.T) {
	cases := []struct {
		name string
		req  *fakeRequester
		sink Sink
		want string
	}{
		{"request", &fakeRequester{pages: 2, perPage: 1, doErr: errors.New("connection refused")}, nil, "page 1: connection refused"},
		{"read", &fakeRequester{pages: 2, perPage: 1, readErr: errors.New("too big")}, nil, "page 1: too big"},
		{"status", &fakeRequester{pages: 3, perPage: 1, failPage: 2}, nil, "page 2: upstream returned 500"},
		{"sink", &fakeRequester{pages: 2, perPage: 1}, func([]json.RawMessage) error { return errors.New("disk full") }, "page 1: disk full"},
		{"cursor unsendable", &fakeRequester{pages: 2, perPage: 1}, nil, "names no cursor_param"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PaginateInput{Items: "data", CursorParam: "cursor"}
			if tc.name == "cursor unsendable" {
				p.CursorParam = ""
			}
			sink := tc.sink
			if sink == nil {
				sink = (&InlineMerge{}).Add
			}
			w := newWalk(t, tc.req, p, sink)
			err := w.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v; want %q", err, tc.want)
			}
		})
	}
	if _, err := New(Options{Paginate: PaginateInput{}, Address: AddressSpec{BaseURL: "https://api.example.com"}}); err == nil {
		t.Error("New accepted an empty paginate block")
	}
	if _, err := New(Options{Paginate: PaginateInput{Items: "data"}, Address: AddressSpec{BaseURL: "nope"}}); err == nil {
		t.Error("New accepted a bad base_url")
	}
}

func TestWalk_RetryAfter(t *testing.T) {
	t.Run("pauses then serves", func(t *testing.T) {
		req := &fakeRequester{pages: 2, perPage: 1, refusals: 1, refuseAs: "0"}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, (&InlineMerge{}).Add)
		if err := w.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if w.Stats.PagesFetched != 2 || len(req.targets) != 3 {
			t.Errorf("stats %+v requests %d", w.Stats, len(req.targets))
		}
	})
	t.Run("bounded", func(t *testing.T) {
		req := &fakeRequester{pages: 2, perPage: 1, refusals: 100, refuseAs: "0"}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, (&InlineMerge{}).Add)
		err := w.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), "429 with Retry-After 11 times in a row") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("past the deadline", func(t *testing.T) {
		req := &fakeRequester{pages: 2, perPage: 1, refusals: 1, refuseAs: "3600"}
		w := newWalk(t, req, PaginateInput{Items: "data", CursorParam: "cursor"}, (&InlineMerge{}).Add)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := w.Run(ctx)
		if err == nil || !strings.Contains(err.Error(), "retry after 1h0m0s") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestExtractItems(t *testing.T) {
	cases := []struct {
		name, body, path string
		wantN            int
		wantErr          string
	}{
		{"top-level key", `{"data":[1,2,3]}`, "data", 3, ""},
		{"nested", `{"r":{"items":[1]}}`, "r.items", 1, ""},
		{"root", `[1,2]`, "$", 2, ""},
		{"absent key is no items", `{"other":1}`, "data", 0, ""},
		{"null is no items", `{"data":null}`, "data", 0, ""},
		{"not an array", `{"data":{"a":1}}`, "data", 0, `items at "data" is not a JSON array`},
		{"root not an array", `{"data":[]}`, "$", 0, `items at "$" is not a JSON array`},
		{"not an object along the path", `[1]`, "data", 0, `not a JSON object at "data"`},
		{"not json", `<html>`, "data", 0, "not a JSON object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := extractItems([]byte(tc.body), PaginateInput{Items: tc.path}.itemsPath())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || len(items) != tc.wantN {
				t.Errorf("items = %d err %v; want %d", len(items), err, tc.wantN)
			}
		})
	}
	if parseJSON([]byte("<html>")) != nil {
		t.Error("parseJSON of non-JSON is not nil")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"2", 2 * time.Second, true},
		{" 0 ", 0, true},
		{"-1", 0, false},
		{"", 0, false},
		{"soon", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = (%s, %v); want (%s, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Retry-After": {"5"}}}
	if _, ok := retryAfterPause(resp, now); ok {
		t.Error("a 200 with Retry-After paused the walk")
	}
	resp.StatusCode = http.StatusServiceUnavailable
	if d, ok := retryAfterPause(resp, now); !ok || d != 5*time.Second {
		t.Errorf("503 pause = (%s, %v)", d, ok)
	}
	resp.Header.Del("Retry-After")
	if _, ok := retryAfterPause(resp, now); ok {
		t.Error("a 503 without Retry-After paused the walk")
	}
}

func TestWaitRetryAfter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := waitRetryAfter(ctx, time.Second); err == nil || !strings.Contains(err.Error(), "past the call's remaining timeout") {
		t.Errorf("err = %v", err)
	}
	if err := waitRetryAfter(context.Background(), 5*time.Millisecond); err != nil {
		t.Errorf("short wait: %v", err)
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := waitRetryAfter(canceled, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled wait: %v", err)
	}
}

func TestInlineMerge_EmptyIsAnEmptyArray(t *testing.T) {
	m := &InlineMerge{Limit: 10}
	if got, _ := json.Marshal(m.Merged()); string(got) != "[]" {
		t.Errorf("Merged() = %s", got)
	}
	if err := m.Add([]json.RawMessage{json.RawMessage("12345"), json.RawMessage("67890")}); !errors.Is(err, ErrPageDoesNotFit) {
		t.Errorf("over-cap page: %v", err)
	}
}
