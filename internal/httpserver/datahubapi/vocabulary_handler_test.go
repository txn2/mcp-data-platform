package datahubapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The create/delete flow is one implementation over both governance
// vocabularies (vocabulary.go), so it is exercised once per kind through this
// table rather than once per kind in its own file. What is genuinely
// kind-specific -- which existing routes each surface reads through -- is tested
// in tags_handler_test.go and domains_handler_test.go.

const (
	vocabStatusTmpl = "%s: status = %d, want %d (%s)"
	vocabDecodeErr  = "%s: decoding response: %v"
)

// vocabCase is one governance vocabulary as its HTTP surface: the route, a name
// to create, the URN that create yields, and URNs a delete must refuse.
type vocabCase struct {
	kind       string
	base       string
	name       string
	urn        string
	badURNs    []string
	created    func(*fakeDataHub) vocabularyRequest
	deletedURN func(*fakeDataHub) string
}

var vocabCases = []vocabCase{
	{
		kind:       "tag",
		base:       "/api/v1/portal/datahub/primary/catalog/tags",
		name:       "certified",
		urn:        "urn:li:tag:certified",
		badURNs:    []string{"", "urn:li:glossaryTerm:revenue", "urn:li:tag:", "certified"},
		created:    func(f *fakeDataHub) vocabularyRequest { return f.createdTag },
		deletedURN: func(f *fakeDataHub) string { return f.deletedTag },
	},
	{
		kind:       fieldDomain,
		base:       "/api/v1/portal/datahub/primary/catalog/domains",
		name:       "finance",
		urn:        "urn:li:domain:finance",
		badURNs:    []string{"", "urn:li:tag:finance", "urn:li:domain:", "finance"},
		created:    func(f *fakeDataHub) vocabularyRequest { return f.createdDomain },
		deletedURN: func(f *fakeDataHub) string { return f.deletedDomain },
	},
}

// TestVocabularyCreate_Succeeds proves a create forwards name and description,
// returns the URN DataHub assigned, and records the mutation in the audit trail
// under the entity type of its own vocabulary.
func TestVocabularyCreate_Succeeds(t *testing.T) {
	for _, tc := range vocabCases {
		t.Run(tc.kind, func(t *testing.T) {
			backend := newFakeDataHub()
			log := &fakeAuditLogger{}
			h := newTestHandler(backend, true, writerResolver(), log)

			rec := serve(h, viewer, "POST", tc.base, `{"name":"`+tc.name+`","description":"reviewed by the data team"}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, http.StatusCreated, rec.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf(vocabDecodeErr, tc.kind, err)
			}
			if got["urn"] != tc.urn {
				t.Errorf("urn = %q, want %q", got["urn"], tc.urn)
			}
			if c := tc.created(backend); c.Name != tc.name || c.Description != "reviewed by the data team" {
				t.Errorf("forwarded %s = %+v", tc.kind, c)
			}
			ev := log.last()
			if ev == nil || ev.ToolName != datahubCreateTool || !ev.Success {
				t.Fatalf("audit event = %+v", ev)
			}
			if ev.Parameters["entity_type"] != tc.kind || ev.Parameters["name"] != tc.name {
				t.Errorf("audit parameters = %+v", ev.Parameters)
			}
		})
	}
}

// TestVocabularyCreate_TrimsAndRequiresName rejects a blank or whitespace-only
// name before any upstream call, and trims the value that is forwarded.
func TestVocabularyCreate_TrimsAndRequiresName(t *testing.T) {
	for _, tc := range vocabCases {
		t.Run(tc.kind, func(t *testing.T) {
			for _, body := range []string{`{"name":""}`, `{"name":"   "}`, `{"description":"no name"}`} {
				backend := newFakeDataHub()
				h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
				rec := serve(h, viewer, "POST", tc.base, body)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("body %s: status = %d, want 400 (%s)", body, rec.Code, rec.Body.String())
				}
				if len(backend.calls) != 0 {
					t.Errorf("body %s: writer must not be called, got %v", body, backend.calls)
				}
			}

			backend := newFakeDataHub()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			if rec := serve(h, viewer, "POST", tc.base, `{"name":"  spaced  ","description":"  padded  "}`); rec.Code != http.StatusCreated {
				t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, http.StatusCreated, rec.Body.String())
			}
			if c := tc.created(backend); c.Name != "spaced" || c.Description != "padded" {
				t.Errorf("forwarded %s = %+v, want trimmed values", tc.kind, c)
			}
		})
	}
}

// TestVocabularyCreate_MalformedBody is a 400, not a forwarded call.
func TestVocabularyCreate_MalformedBody(t *testing.T) {
	for _, tc := range vocabCases {
		t.Run(tc.kind, func(t *testing.T) {
			backend := newFakeDataHub()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "POST", tc.base, `{"name":`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Errorf("writer must not be called, got %v", backend.calls)
			}
		})
	}
}

// TestVocabularyDelete_Succeeds proves the URN reaches the writer and the delete
// is audited under the delete grant.
func TestVocabularyDelete_Succeeds(t *testing.T) {
	for _, tc := range vocabCases {
		t.Run(tc.kind, func(t *testing.T) {
			backend := newFakeDataHub()
			log := &fakeAuditLogger{}
			h := newTestHandler(backend, true, writerResolver(), log)

			rec := serve(h, viewer, "DELETE", tc.base+"?urn="+tc.urn, "")
			if rec.Code != http.StatusOK {
				t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := tc.deletedURN(backend); got != tc.urn {
				t.Errorf("deleted urn = %q, want %q", got, tc.urn)
			}
			ev := log.last()
			if ev == nil || ev.ToolName != datahubDeleteTool || ev.Parameters["urn"] != tc.urn {
				t.Fatalf("audit event = %+v", ev)
			}
			if ev.Parameters["entity_type"] != tc.kind {
				t.Errorf("audit entity_type = %v, want %q", ev.Parameters["entity_type"], tc.kind)
			}
		})
	}
}

// TestVocabularyDelete_RejectsWrongURNKind keeps a URN of another kind from
// reaching DataHub, where it would come back as a misleading 502. Each
// vocabulary refuses the other's URNs, which is what proves the route is bound
// to its own URN types rather than to any URN at all.
func TestVocabularyDelete_RejectsWrongURNKind(t *testing.T) {
	for _, tc := range vocabCases {
		t.Run(tc.kind, func(t *testing.T) {
			for _, urn := range tc.badURNs {
				backend := newFakeDataHub()
				h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
				rec := serve(h, viewer, "DELETE", tc.base+"?urn="+urn, "")
				if rec.Code != http.StatusBadRequest {
					t.Errorf("urn %q: status = %d, want 400 (%s)", urn, rec.Code, rec.Body.String())
				}
				if len(backend.calls) != 0 {
					t.Errorf("urn %q: writer must not be called, got %v", urn, backend.calls)
				}
			}
		})
	}
}

// TestVocabularyWrites_UpstreamFailure surfaces as a 502, and a failed write is
// still audited as a failure so a rejected mutation stays in the trail.
func TestVocabularyWrites_UpstreamFailure(t *testing.T) {
	for _, tc := range vocabCases {
		for _, w := range []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{"create", "POST", tc.base, `{"name":"` + tc.name + `"}`},
			{"delete", "DELETE", tc.base + "?urn=" + tc.urn, ""},
		} {
			t.Run(tc.kind+" "+w.name, func(t *testing.T) {
				backend := newFakeDataHub()
				backend.writeErr = errors.New("datahub down")
				log := &fakeAuditLogger{}
				h := newTestHandler(backend, true, writerResolver(), log)

				rec := serve(h, viewer, w.method, w.path, w.body)
				if rec.Code != http.StatusBadGateway {
					t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, http.StatusBadGateway, rec.Body.String())
				}
				ev := log.last()
				if ev == nil || ev.Success || ev.ErrorMessage == "" {
					t.Fatalf("failed write must be audited as a failure, got %+v", ev)
				}
			})
		}
	}
}

// TestVocabularyWrites_Gated proves each write demands its own grant and a
// write-enabled connection: the reader persona is refused, so is a curator on a
// read-only connection, and so is an anonymous caller.
func TestVocabularyWrites_Gated(t *testing.T) {
	for _, tc := range vocabCases {
		writes := []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{"create", "POST", tc.base, `{"name":"` + tc.name + `"}`},
			{"delete", "DELETE", tc.base + "?urn=" + tc.urn, ""},
		}
		refusals := []struct {
			name     string
			writable bool
			resolver func() portal.PersonaResolver
			user     bool
			want     int
		}{
			{"without the grant", true, readerResolver, true, http.StatusForbidden},
			{"on a read-only connection", false, writerResolver, true, http.StatusForbidden},
			{"unauthenticated", true, writerResolver, false, http.StatusUnauthorized},
		}
		for _, w := range writes {
			for _, ref := range refusals {
				t.Run(tc.kind+" "+w.name+" "+ref.name, func(t *testing.T) {
					backend := newFakeDataHub()
					h := newTestHandler(backend, ref.writable, ref.resolver(), &fakeAuditLogger{})
					caller := viewer
					if !ref.user {
						caller = nil
					}
					rec := serve(h, caller, w.method, w.path, w.body)
					if rec.Code != ref.want {
						t.Fatalf(vocabStatusTmpl, tc.kind, rec.Code, ref.want, rec.Body.String())
					}
					if len(backend.calls) != 0 {
						t.Fatalf("writer must not be called, got %v", backend.calls)
					}
				})
			}
		}
	}
}
