package graphql

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/exporttrunc"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// A walk stopped at its page bound with another page to fetch is a cut file
// (#2057). The upstream here always names another page, so whichever bound
// applies is what stops the walk.

func endlessPages(u *upstream) {
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		return 200, relayPage([]string{fmt.Sprintf("r%d", callNo)}, fmt.Sprintf("c%d", callNo), true)
	}
}

func pagedExport(pag *PaginateInput, opts exporttrunc.Options) exportInput {
	return exportInput{
		Connection: "gql", Query: pagedDocument, Name: "products.json", Paginate: pag, Options: opts,
	}
}

func TestExportWalkAtTheDefaultBoundWritesNothingByDefault(t *testing.T) {
	u := newUpstream(t)
	endlessPages(u)
	tk, assets, blobs := exportToolkit(t, u, "namespaced")

	msg := refuseExport(t, tk, pagedExport(
		&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"}, exporttrunc.Options{}))
	for _, want := range []string{
		"graphql_export: the result was cut at the default page bound of 10 pages (paginate.max_pages unset)",
		"the upstream reported another page, so nothing was written.",
		"Set paginate.max_pages (up to 1000)",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q lacks %q", msg, want)
		}
	}
	if len(blobs.objects) != 0 || len(assets.inserted) != 0 {
		t.Error("a refused walk wrote something")
	}
}

func TestExportWalkAtTheCallersBoundWritesAndFlags(t *testing.T) {
	u := newUpstream(t)
	endlessPages(u)
	tk, assets, _ := exportToolkit(t, u, "namespaced")

	out := callExport(t, tk, pagedExport(
		&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 3}, exporttrunc.Options{}))
	if out.Report == nil || !out.Truncated || out.LimitApplied != 3 || out.LimitSource != exporttrunc.SourceRequest {
		t.Fatalf("report = %+v", out.Report)
	}
	if !strings.Contains(out.Message, "Truncated at the requested limit of 3 pages (paginate.max_pages)") {
		t.Errorf("message = %q", out.Message)
	}
	if len(assets.inserted) != 1 || !slices.Contains(assets.inserted[0].Tags, exporttrunc.Tag) {
		t.Fatalf("the asset is not tagged: %+v", assets.inserted)
	}
	if len(assets.versions) != 1 || exporttrunc.FromMetadata(assets.versions[0].Metadata) == nil {
		t.Fatalf("the version does not record the cut: %+v", assets.versions)
	}
}

func TestExportWalkThatEndsIsComplete(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		return 200, relayPage([]string{fmt.Sprintf("r%d", callNo)}, "c", callNo < 2)
	}
	tk, assets, _ := exportToolkit(t, u, "namespaced")

	out := callExport(t, tk, pagedExport(
		&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2},
		exporttrunc.Options{ExpectRows: new(2)}))
	if out.Report == nil || out.Truncated || out.LimitApplied != 2 {
		t.Fatalf("report = %+v; want complete at the bound", out.Report)
	}
	if slices.Contains(assets.inserted[0].Tags, exporttrunc.Tag) || assets.versions[0].Metadata != nil {
		t.Error("a complete walk is not marked")
	}
}

func TestExportWalkCountExpectationAndValidation(t *testing.T) {
	u := newUpstream(t)
	endlessPages(u)
	tk, _, _ := exportToolkit(t, u, "namespaced")

	msg := refuseExport(t, tk, pagedExport(
		&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2},
		exporttrunc.Options{ExpectMinRows: new(5), OnTruncation: exporttrunc.PolicyFail}))
	if !strings.Contains(msg, "the requested limit of 2 pages") {
		t.Errorf("fail refuses a requested cut too: %q", msg)
	}
	out := callExport(t, tk, pagedExport(
		&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2},
		exporttrunc.Options{ExpectMinRows: new(5), OnTruncation: exporttrunc.PolicyWarn}))
	if out.ExpectMismatch != "expected at least 5 items, got 2" {
		t.Errorf("expect_mismatch = %q", out.ExpectMismatch)
	}
	if msg := refuseExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "x", Options: exporttrunc.Options{ExpectRows: new(1)},
	}); !strings.Contains(msg, "set paginate, or drop them") {
		t.Errorf("an expectation without a walk: %q", msg)
	}
	if msg := refuseExport(t, tk, exportInput{
		Connection: "gql", Query: datasetDocument, Name: "x", Options: exporttrunc.Options{OnTruncation: "drop"},
	}); !strings.Contains(msg, "on_truncation must be") {
		t.Errorf("an invalid on_truncation: %q", msg)
	}
}

func TestExportWalkToAResourceRecordsTheCut(t *testing.T) {
	u := newUpstream(t)
	endlessPages(u)
	tk, _, _ := exportToolkit(t, u, "namespaced")
	lander := newFakeLander()
	tk.exportDeps.ResourceLander = lander

	in := pagedExport(&PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after", MaxPages: 2},
		exporttrunc.Options{})
	in.Resource = &toolkit.ResourceDestination{Path: "catalog", Filename: "products.json"}
	out := callExport(t, tk, in)
	if !out.Truncated || !strings.Contains(out.Message, "This file is incomplete.") {
		t.Fatalf("out = %+v", out)
	}
	if len(lander.landed) != 1 || exporttrunc.FromMetadata(lander.landed[0].Metadata) == nil {
		t.Fatalf("the landing does not record the cut: %+v", lander.landed)
	}
}

func TestExportIdempotencyHitReportsAnEarlierCut(t *testing.T) {
	u := newUpstream(t)
	tk, assets, _ := exportToolkit(t, u, "flat")
	assets.existing = &ExportAssetRef{ID: "prior", Metadata: map[string]any{
		exporttrunc.MetaTruncated: true, exporttrunc.MetaLimitApplied: float64(10),
		exporttrunc.MetaLimitSource: exporttrunc.SourceDeployment, exporttrunc.MetaLimitUnit: exporttrunc.UnitPages,
	}}
	out := callExport(t, tk, exportInput{Connection: "gql", Query: datasetDocument, Name: "x", IdempotencyKey: "k"})
	if out.Report == nil || !out.Truncated ||
		!strings.HasSuffix(out.Message, "That export was truncated at 10 pages; the file is incomplete.") {
		t.Fatalf("out = %+v", out)
	}
}
