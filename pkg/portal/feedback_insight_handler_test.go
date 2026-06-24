package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

type mockMemoryWriter struct {
	inserted *memory.Record
	err      error
}

func (m *mockMemoryWriter) Insert(_ context.Context, rec memory.Record) error {
	m.inserted = &rec
	return m.err
}

func newCaptureHandler(threads ThreadStore, writer MemoryWriter, user *User) *Handler {
	return NewHandler(Deps{
		ThreadStore:  threads,
		MemoryWriter: writer,
		AdminRoles:   []string{"admin"},
		RateLimit:    RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

// a standalone thread is readable by any authenticated user, so the access check
// in captureThreadInsight passes without an asset/page store.
func standaloneThread() *Thread {
	return &Thread{ID: "thr_1", TargetType: targetTypeStandalone, Kind: ThreadKindCorrection, Title: "Revenue excludes returns"}
}

func TestCaptureThreadInsight_Unauthenticated(t *testing.T) {
	h := newCaptureHandler(&mockThreadStore{}, &mockMemoryWriter{}, nil)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestCaptureThreadInsight_RequiresApplyKnowledge(t *testing.T) {
	h := newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, &mockMemoryWriter{}, kpViewer)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestCaptureThreadInsight_ThreadNotFound(t *testing.T) {
	h := newCaptureHandler(&mockThreadStore{getErr: errors.New("missing")}, &mockMemoryWriter{}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestCaptureThreadInsight_CreatesPendingInsightAndLinks(t *testing.T) {
	threads := &mockThreadStore{
		getResult: standaloneThread(),
		events:    []ThreadEvent{{ID: "evt_1", EventType: EventTypeComment, Body: "the amount column excludes returns"}},
	}
	writer := &mockMemoryWriter{}
	h := newCaptureHandler(threads, writer, kpAdmin)

	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}

	rec := writer.inserted
	if rec == nil {
		t.Fatal("no insight record inserted")
	}
	if rec.SinkClass != memory.SinkBusinessKnowledge {
		t.Errorf("sink_class = %q, want business_knowledge", rec.SinkClass)
	}
	if rec.Dimension != memory.SinkClassDimension(memory.SinkBusinessKnowledge) {
		t.Errorf("dimension = %q, want %q", rec.Dimension, memory.SinkClassDimension(memory.SinkBusinessKnowledge))
	}
	if rec.Status != memory.StatusActive {
		t.Errorf("status = %q, want active", rec.Status)
	}
	if rec.Metadata[memory.MetaKeyInsightStatus] != memory.InsightStatusPending {
		t.Errorf("insight_status = %v, want pending", rec.Metadata[memory.MetaKeyInsightStatus])
	}
	if rec.CreatedBy != kpAdmin.Email {
		t.Errorf("created_by = %q, want %q", rec.CreatedBy, kpAdmin.Email)
	}
	// Default content folds the thread title and the first comment body.
	if !strings.Contains(rec.Content, "Revenue excludes returns") || !strings.Contains(rec.Content, "excludes returns") {
		t.Errorf("content did not fold title + first comment: %q", rec.Content)
	}
	// The source thread is linked to the new insight.
	if threads.linkedInsightID != rec.ID || len(threads.linkedThreadIDs) != 1 || threads.linkedThreadIDs[0] != "thr_1" {
		t.Errorf("link not made: insight=%q threads=%v", threads.linkedInsightID, threads.linkedThreadIDs)
	}

	var resp captureInsightResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.InsightID != rec.ID || resp.Status != memory.InsightStatusPending || !resp.Linked {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCaptureThreadInsight_OverridesAndValidation(t *testing.T) {
	t.Run("invalid sink_class", func(t *testing.T) {
		h := newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, &mockMemoryWriter{}, kpAdmin)
		w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
			map[string]any{"content": "a real correction worth keeping", "sink_class": "bogus"})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
	})

	t.Run("rejects a live (non-reviewable) sink_class", func(t *testing.T) {
		writer := &mockMemoryWriter{}
		h := newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, writer, kpAdmin)
		w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
			map[string]any{
				"content":    "this should not become a personal preference",
				"sink_class": memory.SinkPersonalPreference,
			})
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (live sink_class)", w.Code)
		}
		if writer.inserted != nil {
			t.Error("a live sink_class must not insert a record")
		}
	})

	t.Run("explicit content + schema_entity", func(t *testing.T) {
		writer := &mockMemoryWriter{}
		h := newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, writer, kpAdmin)
		w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
			map[string]any{
				"content":     "daily_sales represents one row per store per day",
				"sink_class":  memory.SinkSchemaEntity,
				"entity_urns": []string{"urn:li:dataset:(x)"},
			})
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
		}
		if writer.inserted.SinkClass != memory.SinkSchemaEntity {
			t.Errorf("sink_class = %q, want schema_entity", writer.inserted.SinkClass)
		}
		if len(writer.inserted.EntityURNs) != 1 {
			t.Errorf("entity_urns = %v, want one", writer.inserted.EntityURNs)
		}
	})
}

func TestCaptureThreadInsight_KnowledgePageThread(t *testing.T) {
	threads := &mockThreadStore{getResult: &Thread{
		ID: "thr_1", TargetType: targetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: ThreadKindCorrection, Title: "Fiscal note",
	}}
	pages := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "Fiscal"}}
	writer := &mockMemoryWriter{}
	deps := Deps{
		ThreadStore:        threads,
		MemoryWriter:       writer,
		KnowledgePageStore: pages,
		AdminRoles:         []string{"admin"},
		PersonaResolver:    func([]string) *PersonaInfo { return &PersonaInfo{Name: "curator"} },
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "fiscal year starts in February, not January"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	if writer.inserted.Persona != "curator" {
		t.Errorf("persona = %q, want curator", writer.inserted.Persona)
	}
}

func TestCaptureThreadInsight_KnowledgePageMissing(t *testing.T) {
	threads := &mockThreadStore{getResult: &Thread{
		ID: "thr_1", TargetType: targetTypeKnowledgePage, KnowledgePageID: "gone", Kind: ThreadKindCorrection,
	}}
	deps := Deps{
		ThreadStore:        threads,
		MemoryWriter:       &mockMemoryWriter{},
		KnowledgePageStore: &mockKnowledgePageStore{}, // Get returns ErrNotFound
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "some valid correction text"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (page gone)", w.Code)
	}
}

func TestCaptureThreadInsight_KnowledgePageStoreUnavailable(t *testing.T) {
	threads := &mockThreadStore{getResult: &Thread{
		ID: "thr_1", TargetType: targetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: ThreadKindCorrection,
	}}
	// MemoryWriter present (route registered) but no KnowledgePageStore.
	h := newCaptureHandler(threads, &mockMemoryWriter{}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "a valid correction worth keeping"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (pages not configured)", w.Code)
	}
}

func TestCaptureThreadInsight_KnowledgePageLookupError(t *testing.T) {
	threads := &mockThreadStore{getResult: &Thread{
		ID: "thr_1", TargetType: targetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: ThreadKindCorrection,
	}}
	deps := Deps{
		ThreadStore:        threads,
		MemoryWriter:       &mockMemoryWriter{},
		KnowledgePageStore: &mockKnowledgePageStore{getErr: errors.New("db down")},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "a valid correction worth keeping"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (lookup error)", w.Code)
	}
}

func TestCaptureThreadInsight_InsertError(t *testing.T) {
	writer := &mockMemoryWriter{err: errors.New("db down")}
	h := newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, writer, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "a valid correction worth keeping"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestCaptureThreadInsight_EmptyContentRejected(t *testing.T) {
	// No title and no events -> derived content is empty -> ValidateContent 400.
	threads := &mockThreadStore{getResult: &Thread{ID: "thr_1", TargetType: targetTypeStandalone, Kind: ThreadKindComment}}
	h := newCaptureHandler(threads, &mockMemoryWriter{}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty content)", w.Code)
	}
}

func TestCaptureThreadInsight_NotRegisteredWithoutWriter(t *testing.T) {
	h := NewHandler(Deps{
		ThreadStore: &mockThreadStore{getResult: standaloneThread()},
		AdminRoles:  []string{"admin"},
		RateLimit:   RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(kpAdmin))
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route absent without MemoryWriter)", w.Code)
	}
}
