package apigateway

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// A walk stopped at its page bound with another page to fetch is a cut file
// (#2057). These run a real walk against a paged upstream with more pages
// than the bound.

func cutWalkInput(pag PaginateInput, opts exporttrunc.Options) exportInput {
	return exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/contacts", Name: "contacts.json",
		Paginate: &pag, Options: opts,
	}
}

func TestExportWalkAtTheDefaultBoundWritesNothingByDefault(t *testing.T) {
	// One page more than the default bound, so the default is what stops it.
	up := (&pagedUpstream{t: t, pages: 101, perPage: 1, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)

	res, out := exportWalk(t, tk, cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor"}, exporttrunc.Options{}))
	if !res.IsError || out != nil {
		t.Fatalf("a walk the default bound cut must be refused; got %s", renderedText(t, res))
	}
	text := resultText(t, res)
	for _, want := range []string{
		"the default page bound of 100 pages (paginate.max_pages unset)",
		"the upstream reported another page, so nothing was written.",
		"Set paginate.max_pages (up to 10000)",
		"To write the first 100 pages anyway, marked incomplete, set on_truncation to",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal %q lacks %q", text, want)
		}
	}
	if len(store.inserted) != 0 || len(s3.puts) != 0 {
		t.Errorf("a refused walk wrote %d asset rows and %d objects; want none", len(store.inserted), len(s3.puts))
	}
}

func TestExportWalkAtTheCallersBoundWritesAndFlags(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 5, perPage: 2, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	versions := &fakeExportVersionStore{}
	deps := defaultExportDeps(store, versions, s3)
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)

	res, out := exportWalk(t, tk, cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 2}, exporttrunc.Options{}))
	if res.IsError {
		t.Fatalf("a walk cut by the caller's own bound is written: %s", resultText(t, res))
	}
	if out.Report == nil || !out.Truncated || out.LimitApplied != 2 || out.LimitSource != exporttrunc.SourceRequest ||
		out.LimitUnit != exporttrunc.UnitPages {
		t.Fatalf("report = %+v; want truncated at the requested 2 pages", out.Report)
	}
	if out.ArbitrarySubset {
		t.Error("a page walk keeps the upstream's order, so it makes no claim about an arbitrary subset")
	}
	if !strings.Contains(out.Message, "Truncated at the requested limit of 2 pages (paginate.max_pages); the upstream reported another page. This file is incomplete.") {
		t.Errorf("message = %q", out.Message)
	}
	if len(store.inserted) != 1 || !containsString(store.inserted[0].Tags, exporttrunc.Tag) {
		t.Fatalf("the asset is not tagged: %+v", store.inserted)
	}
	if len(versions.createdVersions) != 1 || versions.createdVersions[0].Metadata[exporttrunc.MetaLimitUnit] != exporttrunc.UnitPages {
		t.Fatalf("the version does not record the cut: %+v", versions.createdVersions)
	}
	// The wire form a client reads carries the fields.
	var wire map[string]any
	if err := json.Unmarshal([]byte(renderedText(t, res)), &wire); err != nil {
		t.Fatal(err)
	}
	if truncated, _ := wire["truncated"].(bool); !truncated || wire["limit_source"] != exporttrunc.SourceRequest || wire["stopped_by"] != "max_pages" {
		t.Errorf("wire = %v", wire)
	}
}

func TestExportWalkThatEndsIsComplete(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 2, perPage: 2, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	versions := &fakeExportVersionStore{}
	deps := defaultExportDeps(store, versions, s3)
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)

	// Exactly at the bound: two pages, the second with no next signal.
	res, out := exportWalk(t, tk, cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 2},
		exporttrunc.Options{ExpectRows: new(4)}))
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	if out.Truncated || out.LimitApplied != 2 || out.StoppedBy != "end" {
		t.Fatalf("report = %+v stats = %+v; want complete", out.Report, out.WalkStats)
	}
	if containsString(store.inserted[0].Tags, exporttrunc.Tag) || versions.createdVersions[0].Metadata != nil {
		t.Error("a complete walk is not marked")
	}
}

func TestExportWalkUnderWarnAtTheDefaultBoundWritesAndFlags(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 101, perPage: 1, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)

	res, out := exportWalk(t, tk, cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor"},
		exporttrunc.Options{OnTruncation: exporttrunc.PolicyWarn}))
	if res.IsError {
		t.Fatalf("warn writes the pages fetched: %s", resultText(t, res))
	}
	if !out.Truncated || out.LimitSource != exporttrunc.SourceDeployment || out.ItemsMerged != 100 {
		t.Fatalf("report = %+v stats = %+v", out.Report, out.WalkStats)
	}
	assertSequence(t, decodeMergedIDs(t, s3.puts[0].Data), 100)
}

func TestExportWalkCountExpectation(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 2, perPage: 2, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	tk := walkExportToolkit(t, up, store, s3)

	res, _ := exportWalk(t, tk, cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor"},
		exporttrunc.Options{ExpectMinRows: new(10)}))
	if !res.IsError || !strings.Contains(resultText(t, res), "expected at least 10 items, got 4; nothing was written.") {
		t.Fatalf("a missed minimum must be refused: %s", renderedText(t, res))
	}
	if len(store.inserted) != 0 {
		t.Error("a refused walk wrote an asset")
	}
}

func TestExportExpectationNeedsAWalk(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 1, perPage: 1, mode: "cursor"}).start()
	tk := walkExportToolkit(t, up, &fakeExportAssetStore{}, &fakeExportS3Client{})
	res, _ := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/contacts", Name: "c.json",
		Options: exporttrunc.Options{ExpectRows: new(1)},
	})
	if !res.IsError || !strings.Contains(resultText(t, res), "set paginate, or drop them") {
		t.Fatalf("an expectation on a single response must be refused: %s", renderedText(t, res))
	}
	res, _ = exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/contacts", Name: "c.json",
		Options: exporttrunc.Options{OnTruncation: "skip"},
	})
	if !res.IsError || !strings.Contains(resultText(t, res), "on_truncation must be") {
		t.Fatalf("an invalid on_truncation must be refused: %s", renderedText(t, res))
	}
}

func TestExportWalkToAResourceRecordsTheCut(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 5, perPage: 1, mode: "cursor"}).start()
	lander := newFakeLander()
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps.ResourceLander = lander
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)

	in := cutWalkInput(PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 3}, exporttrunc.Options{})
	in.Resource = &toolkit.ResourceDestination{Path: "lists", Filename: "contacts.json"}
	res, out := exportWalk(t, tk, in)
	if res.IsError {
		t.Fatalf("export failed: %s", resultText(t, res))
	}
	if !out.Truncated || !strings.Contains(out.Message, "This file is incomplete.") {
		t.Fatalf("output = %+v", out)
	}
	if len(lander.landed) != 1 || exporttrunc.FromMetadata(lander.landed[0].Metadata) == nil {
		t.Fatalf("the landing does not record the cut: %+v", lander.landed)
	}

	// The default bound refuses a resource landing too: the stream breaks and
	// the lander writes nothing.
	lander.landed = nil
	in.Paginate = &PaginateInput{Items: "data", CursorParam: "cursor"}
	up100 := (&pagedUpstream{t: t, pages: 101, perPage: 1, mode: "cursor"}).start()
	deps100 := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, &fakeExportS3Client{})
	deps100.ResourceLander = lander
	tk100 := buildExportTestToolkit(t, up100.srv.URL, &deps100)
	res, _ = exportWalk(t, tk100, in)
	if !res.IsError || len(lander.landed) != 0 {
		t.Fatalf("a refused walk landed %d files: %s", len(lander.landed), renderedText(t, res))
	}
}

func TestExportIdempotencyHitReportsAnEarlierCut(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 1, perPage: 1, mode: "cursor"}).start()
	store := &fakeExportAssetStore{}
	tk := walkExportToolkit(t, up, store, &fakeExportS3Client{})
	store.idempLookups = map[string]*ExportAssetRef{"u1:k": {ID: "a1", Metadata: map[string]any{
		exporttrunc.MetaTruncated: true, exporttrunc.MetaLimitApplied: float64(3),
		exporttrunc.MetaLimitSource: exporttrunc.SourceRequest, exporttrunc.MetaLimitUnit: exporttrunc.UnitPages,
	}}}
	res, out := exportWalk(t, tk, exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/contacts", Name: "c.json", IdempotencyKey: "k",
	})
	if res.IsError || out.Report == nil || !out.Truncated {
		t.Fatalf("hit = %s", renderedText(t, res))
	}
	if !strings.HasSuffix(out.Message, "That export was truncated at 3 pages; the file is incomplete.") {
		t.Errorf("message = %q", out.Message)
	}
}

func containsString(list []string, want string) bool {
	return slices.Contains(list, want)
}
