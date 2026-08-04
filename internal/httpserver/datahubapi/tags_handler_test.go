package datahubapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// The tag create and delete themselves are exercised in
// vocabulary_handler_test.go, which runs the one shared implementation over both
// governance vocabularies. What is left here is the part that is specific to
// tags: which pre-existing routes the Tags surface reads through.

const (
	testTagURN     = "urn:li:tag:certified"
	tagStatusTmpl  = "status = %d, want %d (%s)"
	tagDecodeError = "decoding response: %v"
)

// TestTagReadsServedByExistingRoutes pins the reuse the Tags surface depends on:
// the tag list is the picker's lookup route, the datasets carrying a tag come
// from the catalog search's tags filter, and the description edit is the shared
// entity-description route. A rename of any of them breaks the Tags tab, so each
// is asserted here rather than only in the picker's own tests.
func TestTagReadsServedByExistingRoutes(t *testing.T) {
	backend := newFakeDataHub()
	backend.refs = []semantic.EntityRef{{URN: testTagURN, Name: "certified", Description: "reviewed"}}
	backend.tables = []semantic.TableSearchResult{{URN: dhTestURN, Name: "orders"}}
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/lookup/tags?q=cert&limit=200", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("tag list: "+tagStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var list struct {
		Results []semantic.EntityRef `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf(tagDecodeError, err)
	}
	if len(list.Results) != 1 || list.Results[0].Description != "reviewed" {
		t.Errorf("tag list = %+v, want the description carried through", list.Results)
	}

	rec = serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/search?q=*&tags="+testTagURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("tag usage: "+tagStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var usage struct {
		Results []semantic.TableSearchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatalf(tagDecodeError, err)
	}
	if len(usage.Results) != 1 || usage.Results[0].URN != dhTestURN {
		t.Errorf("tag usage = %+v", usage.Results)
	}

	rec = serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description",
		`{"urn":"`+testTagURN+`","description":"now documented"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tag description: "+tagStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	if backend.descriptions[testTagURN] != "now documented" {
		t.Errorf("tag description = %q", backend.descriptions[testTagURN])
	}
}
