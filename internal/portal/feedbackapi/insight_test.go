package feedbackapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

type mockMemoryWriter struct {
	inserted *memory.Record
	err      error
}

func (m *mockMemoryWriter) Insert(_ context.Context, rec memory.Record) error {
	m.inserted = &rec
	return m.err
}

func newCaptureHandler(store threads.ThreadStore, writer MemoryWriter, user *access.User) http.Handler {
	return newTestServer(Config{Threads: store, MemoryWriter: writer}, user)
}

// a standalone thread is readable by any authenticated user, so the access check
// in captureThreadInsight passes without an asset/page store.
func standaloneThread() *threads.Thread {
	return &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, Kind: threads.ThreadKindCorrection, Title: "Revenue excludes returns"}
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
	store := &mockThreadStore{
		getResult: standaloneThread(),
		events:    []threads.ThreadEvent{{ID: "evt_1", EventType: threads.EventTypeComment, Body: "the amount column excludes returns"}},
	}
	writer := &mockMemoryWriter{}
	h := newCaptureHandler(store, writer, kpAdmin)

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
	if store.linkedInsightID != rec.ID || len(store.linkedThreadIDs) != 1 || store.linkedThreadIDs[0] != "thr_1" {
		t.Errorf("link not made: insight=%q threads=%v", store.linkedInsightID, store.linkedThreadIDs)
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
	store := &mockThreadStore{getResult: &threads.Thread{
		ID: "thr_1", TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: threads.ThreadKindCorrection, Title: "Fiscal note",
	}}
	pages := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "Fiscal"}}
	writer := &mockMemoryWriter{}
	cfg := Config{
		Threads:        store,
		MemoryWriter:   writer,
		KnowledgePages: pages,
		PersonaName:    func([]string) string { return "curator" },
	}
	h := newTestServer(cfg, kpAdmin)
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
	store := &mockThreadStore{getResult: &threads.Thread{
		ID: "thr_1", TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "gone", Kind: threads.ThreadKindCorrection,
	}}
	cfg := Config{
		Threads:        store,
		MemoryWriter:   &mockMemoryWriter{},
		KnowledgePages: &mockKnowledgePageStore{}, // Get returns ErrNotFound
	}
	h := newTestServer(cfg, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "some valid correction text"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (page gone)", w.Code)
	}
}

func TestCaptureThreadInsight_KnowledgePageStoreUnavailable(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{
		ID: "thr_1", TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: threads.ThreadKindCorrection,
	}}
	// MemoryWriter present (route registered) but no KnowledgePageStore.
	h := newCaptureHandler(store, &mockMemoryWriter{}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "a valid correction worth keeping"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (pages not configured)", w.Code)
	}
}

func TestCaptureThreadInsight_KnowledgePageLookupError(t *testing.T) {
	store := &mockThreadStore{getResult: &threads.Thread{
		ID: "thr_1", TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "kp1", Kind: threads.ThreadKindCorrection,
	}}
	cfg := Config{
		Threads:        store,
		MemoryWriter:   &mockMemoryWriter{},
		KnowledgePages: &mockKnowledgePageStore{getErr: errors.New("db down")},
	}
	h := newTestServer(cfg, kpAdmin)
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
	store := &mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, Kind: threads.ThreadKindComment}}
	h := newCaptureHandler(store, &mockMemoryWriter{}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (empty content)", w.Code)
	}
}

func TestCaptureThreadInsight_NotRegisteredWithoutWriter(t *testing.T) {
	h := newTestServer(Config{Threads: &mockThreadStore{getResult: standaloneThread()}}, kpAdmin)
	w := doThreadReq(t, h, "POST", "/api/v1/portal/threads/thr_1/insight", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route absent without MemoryWriter)", w.Code)
	}
}

// Test principals: an admin, and a viewer whose persona grants no capability.
var (
	kpAdmin  = &access.User{UserID: "admin-1", Email: "admin@example.com", Roles: []string{"admin"}}
	kpViewer = &access.User{UserID: "viewer-1", Email: "viewer@example.com", Roles: []string{"analyst"}}
)

// captureLog collects what the package logs while a test runs.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func longFeedbackThread(body string) *mockThreadStore {
	return &mockThreadStore{
		getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeStandalone, Kind: threads.ThreadKindCorrection},
		events:    []threads.ThreadEvent{{ID: "evt_1", EventType: threads.EventTypeComment, Body: body}},
	}
}

func TestCaptureThreadInsight_DefaultOverTheLimitIsRefusedAndNotCut(t *testing.T) {
	log := captureLog(t)
	secret := "acc-marker-" + strings.Repeat("x", memory.MaxContentLen)
	store := longFeedbackThread(secret)
	writer := &mockMemoryWriter{}
	w := doThreadReq(t, newCaptureHandler(store, writer, kpAdmin), "POST", "/api/v1/portal/threads/thr_1/insight", nil)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	want := "content must be at most 4000 bytes (got 4011)"
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("the refusal does not say why: %s", w.Body.String())
	}
	if writer.inserted != nil {
		t.Errorf("an insight was written: %d bytes", len(writer.inserted.Content))
	}
	if store.linkedInsightID != "" {
		t.Errorf("the thread was resolved against %q", store.linkedInsightID)
	}
	got := log.String()
	if !strings.Contains(got, want) || !strings.Contains(got, "thr_1") {
		t.Errorf("the log does not hold the reason: %s", got)
	}
	if strings.Contains(got, "acc-marker") {
		t.Errorf("the log holds the feedback's text: %s", got)
	}
}

func TestCaptureThreadInsight_ShortenedTextIsCapturedAsSent(t *testing.T) {
	store := longFeedbackThread(strings.Repeat("x", memory.MaxContentLen+1))
	writer := &mockMemoryWriter{}
	shortened := strings.Repeat("y", memory.MaxContentLen)
	w := doThreadReq(t, newCaptureHandler(store, writer, kpAdmin), "POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": "  " + shortened + "\n"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	if writer.inserted == nil || writer.inserted.Content != shortened {
		t.Fatalf("the insight does not hold the reviewer's text")
	}
	if store.linkedInsightID != writer.inserted.ID {
		t.Errorf("the thread was not linked: %q", store.linkedInsightID)
	}
}

func TestCaptureThreadInsight_TextOverTheLimitIsRefused(t *testing.T) {
	writer := &mockMemoryWriter{}
	w := doThreadReq(t, newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, writer, kpAdmin),
		"POST", "/api/v1/portal/threads/thr_1/insight",
		map[string]any{"content": strings.Repeat("é", memory.MaxContentLen/2+1)})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if writer.inserted != nil {
		t.Error("text over the limit was written")
	}
}

func TestCaptureThreadInsight_EveryRefusalIsLogged(t *testing.T) {
	cases := []struct {
		name string
		h    http.Handler
		code int
		text string
	}{
		{"unauthenticated", newCaptureHandler(&mockThreadStore{}, &mockMemoryWriter{}, nil), http.StatusUnauthorized, errAuthRequired},
		{"without apply_knowledge", newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, &mockMemoryWriter{}, kpViewer), http.StatusForbidden, "requires apply_knowledge"},
		{"unknown thread, with the cause", newCaptureHandler(&mockThreadStore{getErr: errors.New("db gone")}, &mockMemoryWriter{}, kpAdmin), http.StatusNotFound, "db gone"},
		{"unreachable target", newCaptureHandler(&mockThreadStore{getResult: &threads.Thread{ID: "thr_1", TargetType: portaldomain.TargetTypeKnowledgePage, KnowledgePageID: "kp1"}}, &mockMemoryWriter{}, kpAdmin), http.StatusServiceUnavailable, "knowledge pages not configured"},
		{"failed write, with the cause", newCaptureHandler(&mockThreadStore{getResult: standaloneThread()}, &mockMemoryWriter{err: errors.New("insert broke")}, kpAdmin), http.StatusInternalServerError, "insert broke"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := captureLog(t)
			w := doThreadReq(t, tc.h, "POST", "/api/v1/portal/threads/thr_1/insight", map[string]any{"content": "a correction worth keeping"})
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.code, w.Body.String())
			}
			if got := log.String(); !strings.Contains(got, "capture as insight refused") || !strings.Contains(got, tc.text) {
				t.Errorf("the log does not hold %q: %s", tc.text, got)
			}
			if strings.Contains(log.String(), "a correction worth keeping") {
				t.Errorf("the log holds the insight's text: %s", log.String())
			}
		})
	}
}
