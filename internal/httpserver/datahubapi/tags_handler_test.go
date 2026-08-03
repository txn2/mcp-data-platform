package datahubapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	tagsBase       = "/api/v1/portal/datahub/primary/catalog/tags"
	testTagURN     = "urn:li:tag:certified"
	tagStatusTmpl  = "status = %d, want %d (%s)"
	tagDecodeError = "decoding response: %v"
)

// TestCreateTag_Succeeds proves a create forwards name and description, returns
// the URN DataHub assigned, and records the mutation in the audit trail.
func TestCreateTag_Succeeds(t *testing.T) {
	backend := newFakeDataHub()
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	rec := serve(h, viewer, "POST", tagsBase, `{"name":"certified","description":"reviewed by the data team"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(tagDecodeError, err)
	}
	if got["urn"] != testTagURN {
		t.Errorf("urn = %q, want %q", got["urn"], testTagURN)
	}
	if backend.createdTag.Name != "certified" || backend.createdTag.Description != "reviewed by the data team" {
		t.Errorf("forwarded tag = %+v", backend.createdTag)
	}
	ev := log.last()
	if ev == nil || ev.ToolName != datahubCreateTool || !ev.Success {
		t.Fatalf("audit event = %+v", ev)
	}
	if ev.Parameters["entity_type"] != "tag" || ev.Parameters["name"] != "certified" {
		t.Errorf("audit parameters = %+v", ev.Parameters)
	}
}

// TestCreateTag_TrimsAndRequiresName rejects a blank or whitespace-only name
// before any upstream call, and trims the value that is forwarded.
func TestCreateTag_TrimsAndRequiresName(t *testing.T) {
	for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{"description":"no name"}`} {
		backend := newFakeDataHub()
		h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
		rec := serve(h, viewer, "POST", tagsBase, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400 (%s)", body, rec.Code, rec.Body.String())
		}
		if len(backend.calls) != 0 {
			t.Errorf("body %s: writer must not be called, got %v", body, backend.calls)
		}
	}

	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "POST", tagsBase, `{"name":"  spaced  ","description":"  padded  "}`); rec.Code != http.StatusCreated {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
	}
	if backend.createdTag.Name != "spaced" || backend.createdTag.Description != "padded" {
		t.Errorf("forwarded tag = %+v, want trimmed values", backend.createdTag)
	}
}

// TestCreateTag_MalformedBody is a 400, not a forwarded call.
func TestCreateTag_MalformedBody(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "POST", tagsBase, `{"name":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Errorf("writer must not be called, got %v", backend.calls)
	}
}

// TestCreateTag_UpstreamFailure surfaces as a 502 and is audited as a failure,
// so a rejected write is still in the trail.
func TestCreateTag_UpstreamFailure(t *testing.T) {
	backend := newFakeDataHub()
	backend.writeErr = errors.New("datahub down")
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	rec := serve(h, viewer, "POST", tagsBase, `{"name":"certified"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	ev := log.last()
	if ev == nil || ev.Success || ev.ErrorMessage == "" {
		t.Fatalf("failed write must be audited as a failure, got %+v", ev)
	}
}

// TestDeleteTag_Succeeds proves the URN reaches the writer and the delete is
// audited under the delete grant.
func TestDeleteTag_Succeeds(t *testing.T) {
	backend := newFakeDataHub()
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	rec := serve(h, viewer, "DELETE", tagsBase+"?urn="+testTagURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	if backend.deletedTag != testTagURN {
		t.Errorf("deleted urn = %q, want %q", backend.deletedTag, testTagURN)
	}
	ev := log.last()
	if ev == nil || ev.ToolName != datahubDeleteTool || ev.Parameters["urn"] != testTagURN {
		t.Fatalf("audit event = %+v", ev)
	}
}

// TestDeleteTag_RejectsNonTagURN keeps a URN of the wrong kind from reaching
// DataHub, where it would come back as a misleading 502.
func TestDeleteTag_RejectsNonTagURN(t *testing.T) {
	for _, urn := range []string{"", "urn:li:glossaryTerm:revenue", "urn:li:tag:", "certified"} {
		backend := newFakeDataHub()
		h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
		rec := serve(h, viewer, "DELETE", tagsBase+"?urn="+urn, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("urn %q: status = %d, want 400 (%s)", urn, rec.Code, rec.Body.String())
		}
		if len(backend.calls) != 0 {
			t.Errorf("urn %q: writer must not be called, got %v", urn, backend.calls)
		}
	}
}

// TestDeleteTag_UpstreamFailure surfaces as a 502.
func TestDeleteTag_UpstreamFailure(t *testing.T) {
	backend := newFakeDataHub()
	backend.writeErr = errors.New("datahub down")
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "DELETE", tagsBase+"?urn="+testTagURN, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(tagStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestTagWrites_Gated proves each tag write demands its own grant and a
// write-enabled connection: the reader persona is refused, and so is a curator
// on a read-only connection.
func TestTagWrites_Gated(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create", "POST", tagsBase, `{"name":"certified"}`},
		{"delete", "DELETE", tagsBase + "?urn=" + testTagURN, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name+" without the grant", func(t *testing.T) {
			backend := newFakeDataHub()
			h := newTestHandler(backend, true, readerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf(tagStatusTmpl, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Fatalf("writer must not be called, got %v", backend.calls)
			}
		})
		t.Run(tc.name+" on a read-only connection", func(t *testing.T) {
			backend := newFakeDataHub()
			h := newTestHandler(backend, false, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf(tagStatusTmpl, rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Fatalf("writer must not be called, got %v", backend.calls)
			}
		})
		t.Run(tc.name+" unauthenticated", func(t *testing.T) {
			backend := newFakeDataHub()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, nil, tc.method, tc.path, tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf(tagStatusTmpl, rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		})
	}
}

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
