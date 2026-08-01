package datahubapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

func newTestClientWriter(t *testing.T, url string) *knowledgekit.DataHubClientWriter {
	t.Helper()
	return knowledgekit.NewDataHubClientWriter(newTestClient(t, url))
}

func TestStaticBridge(t *testing.T) {
	b := NewStaticBridge()
	if !b.Empty() {
		t.Fatal("new bridge should be empty")
	}
	backend := newFakeDataHub()
	b.Add("rw", backend, backend)
	b.Add("ro", backend, nil)

	if b.Empty() {
		t.Fatal("bridge should not be empty after Add")
	}
	conns := b.Connections()
	if len(conns) != 2 || conns[0].Name != "rw" || !conns[0].Writable || conns[1].Name != "ro" || conns[1].Writable {
		t.Fatalf("unexpected connections: %+v", conns)
	}
	if _, ok := b.Reader("rw"); !ok {
		t.Error("rw reader missing")
	}
	if _, ok := b.Writer("rw"); !ok {
		t.Error("rw writer missing")
	}
	if _, ok := b.Writer("ro"); ok {
		t.Error("ro should have no writer")
	}
	if _, ok := b.Reader("nope"); ok {
		t.Error("unknown reader should be absent")
	}
}

func newTestClient(t *testing.T, url string) *dhclient.Client {
	t.Helper()
	cfg := dhclient.DefaultConfig()
	cfg.URL = url
	cfg.Token = "t"
	cfg.RetryMax = 0
	c, err := dhclient.New(cfg)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c
}

func TestBuildConnection(t *testing.T) {
	c := newTestClient(t, "http://datahub.invalid")

	// writable
	reader, writer, err := BuildConnection(c, "trino", nil, false)
	if err != nil || reader == nil || writer == nil {
		t.Fatalf("writable build: reader=%v writer=%v err=%v", reader, writer, err)
	}
	// read-only -> nil writer
	reader, writer, err = BuildConnection(c, "trino", map[string]string{"a": "b"}, true)
	if err != nil || reader == nil || writer != nil {
		t.Fatalf("read-only build: reader=%v writer=%v err=%v", reader, writer, err)
	}
}

func TestContextDocToDocumentResult(t *testing.T) {
	if contextDocToDocumentResult(nil) != nil {
		t.Fatal("nil input should map to nil")
	}
	got := contextDocToDocumentResult(&types.ContextDocument{ID: "abc", Title: "T", Content: "B", Category: "note"})
	if got.URN != "urn:li:document:abc" || got.Title != "T" || got.Body != "B" || got.SubType != "note" {
		t.Fatalf("unexpected mapping: %+v", got)
	}
}

// TestClientWriter_Delegates exercises the wrapper's success path (update-path
// upsert) against a permissive server, plus the DTO conversions.
func TestClientWriter_Delegates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"value":{"tags":[],"terms":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"document":{"urn":"urn:li:document:d1"},"addOwner":true,"removeOwner":true,"setDomain":true,"unsetDomain":true}}`))
	}))
	defer server.Close()

	cw := clientWriter{w: newTestClientWriter(t, server.URL)}
	ctx := context.Background()
	urn := "urn:li:dataset:(urn:li:dataPlatform:trino,c.s.t,PROD)"

	_ = cw.ApplyOwnerChanges(ctx, urn, []OwnerChange{{OwnerURN: "urn:li:corpuser:a", OwnershipType: "TECHNICAL_OWNER"}}, nil)
	_ = cw.UpdateDescription(ctx, urn, "d")
	_ = cw.ApplyTagChanges(ctx, urn, []string{"urn:li:tag:X"}, nil)
	_ = cw.ApplyGlossaryTermChanges(ctx, urn, []string{"urn:li:glossaryTerm:Y"}, nil)
	_ = cw.SetDomain(ctx, urn, "urn:li:domain:d")
	_ = cw.UnsetDomain(ctx, urn)
	doc, err := cw.UpsertContextDocument(ctx, DocumentInput{ID: "d1", EntityURN: urn, Title: "T", Content: "C"})
	if err != nil || doc == nil {
		t.Fatalf("upsert (update path) should succeed: doc=%+v err=%v", doc, err)
	}
	_ = cw.DeleteContextDocument(ctx, "d1")
}

// TestClientWriter_ErrorsWrapped covers each wrapper method's error branch.
func TestClientWriter_ErrorsWrapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cw := clientWriter{w: newTestClientWriter(t, server.URL)}
	ctx := context.Background()
	urn := "urn:li:dataset:(urn:li:dataPlatform:trino,c.s.t,PROD)"

	checks := map[string]func() error{
		"UpdateDescription":        func() error { return cw.UpdateDescription(ctx, urn, "d") },
		"ApplyTagChanges":          func() error { return cw.ApplyTagChanges(ctx, urn, []string{"urn:li:tag:X"}, nil) },
		"ApplyGlossaryTermChanges": func() error { return cw.ApplyGlossaryTermChanges(ctx, urn, []string{"urn:li:glossaryTerm:Y"}, nil) },
		"ApplyOwnerChanges": func() error {
			return cw.ApplyOwnerChanges(ctx, urn, []OwnerChange{{OwnerURN: "urn:li:corpuser:a"}}, nil)
		},
		"SetDomain":             func() error { return cw.SetDomain(ctx, urn, "urn:li:domain:d") },
		"UnsetDomain":           func() error { return cw.UnsetDomain(ctx, urn) },
		"DeleteContextDocument": func() error { return cw.DeleteContextDocument(ctx, "d1") },
		"UpsertContextDocument": func() error {
			_, e := cw.UpsertContextDocument(ctx, DocumentInput{EntityURN: urn, Title: "T", Content: "C"})
			return e
		},
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s: expected a wrapped error", name)
		}
	}
}
