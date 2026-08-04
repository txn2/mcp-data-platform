package datahubapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// The domain create and delete themselves are exercised in
// vocabulary_handler_test.go, which runs the one shared implementation over both
// governance vocabularies. What is left here is specific to domains: which
// pre-existing routes the Domains surface reads and writes through, including
// the membership edit, which has a direction a shared test cannot express.

const (
	testDomainURN    = "urn:li:domain:finance"
	domainStatusTmpl = "status = %d, want %d (%s)"
	domainDecodeErr  = "decoding response: %v"
)

// TestDomainReadsServedByExistingRoutes pins the reuse the Domains surface
// depends on: the domain list is the picker's lookup route, the tables in a
// domain come from the catalog search's domain filter, and the description edit
// is the shared entity-description route. A rename of any of them breaks the
// Domains tab, so each is asserted here rather than only in the picker's own
// tests.
func TestDomainReadsServedByExistingRoutes(t *testing.T) {
	backend := newFakeDataHub()
	backend.refs = []semantic.EntityRef{{URN: testDomainURN, Name: "Finance", Description: "Revenue and billing"}}
	backend.tables = []semantic.TableSearchResult{{URN: dhTestURN, Name: "orders"}}
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/lookup/domains", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("domain list: "+domainStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var list struct {
		Results []semantic.EntityRef `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf(domainDecodeErr, err)
	}
	if len(list.Results) != 1 || list.Results[0].Description != "Revenue and billing" {
		t.Errorf("domain list = %+v, want the description carried through", list.Results)
	}

	rec = serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/search?q=*&domain="+testDomainURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("domain membership: "+domainStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var members struct {
		Results []semantic.TableSearchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
		t.Fatalf(domainDecodeErr, err)
	}
	if len(members.Results) != 1 || members.Results[0].URN != dhTestURN {
		t.Errorf("domain membership = %+v", members.Results)
	}

	rec = serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description",
		`{"urn":"`+testDomainURN+`","description":"now documented"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("domain description: "+domainStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	if backend.descriptions[testDomainURN] != "now documented" {
		t.Errorf("domain description = %q", backend.descriptions[testDomainURN])
	}
}

// TestDomainMembershipServedByEntityDomainRoute pins the other half of the
// reuse: adding a table to a domain and removing it are the entity editor's
// domain write, aimed at the table rather than at the domain. The Domains tab
// makes exactly these two calls, so the direction of each argument is asserted
// here — a swapped entity/domain URN would still return 200.
func TestDomainMembershipServedByEntityDomainRoute(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/domain",
		`{"urn":"`+dhTestURN+`","domain":"`+testDomainURN+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("domain add: "+domainStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	if backend.setDomain != [2]string{dhTestURN, testDomainURN} {
		t.Errorf("set domain = %v, want the table URN first and the domain URN second", backend.setDomain)
	}

	rec = serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/domain",
		`{"urn":"`+dhTestURN+`","clear_domain":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("domain remove: "+domainStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	if backend.unsetDomain != dhTestURN {
		t.Errorf("unset domain = %q, want %q", backend.unsetDomain, dhTestURN)
	}
}
