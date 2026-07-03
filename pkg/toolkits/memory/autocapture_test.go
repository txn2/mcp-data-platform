package memory

import (
	"context"
	"testing"

	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
)

func TestAutoCapture_PersistsReviewedCorrection(t *testing.T) {
	store := &mockStore{}
	tk := newTestToolkit(store, nil)

	res, err := tk.AutoCapture(context.Background(), AutoCaptureInput{
		SinkClass:  memstore.SinkSchemaEntity,
		Content:    "A query error was corrected: custmer_id -> customer_id on sales.orders.",
		Category:   memstore.CategoryCorrection,
		EntityURNs: []string{"urn:li:dataset:(urn:li:dataPlatform:trino,cat.sales.orders,PROD)"},
		CreatedBy:  "analyst@example.com",
		Persona:    "analyst",
		SessionID:  "sess-abc",
		Metadata:   map[string]any{"reflexive_trigger": "query_error_fix"},
	})
	if err != nil {
		t.Fatalf("AutoCapture: %v", err)
	}
	if len(store.insertedRecords) != 1 {
		t.Fatalf("expected 1 inserted record, got %d", len(store.insertedRecords))
	}

	rec := store.insertedRecords[0]
	if rec.ID != res.ID || rec.ID == "" {
		t.Errorf("record ID mismatch: rec=%q res=%q", rec.ID, res.ID)
	}
	if rec.Source != memstore.SourceAutomation {
		t.Errorf("Source = %q, want automation (default for server-initiated capture)", rec.Source)
	}
	if rec.CreatedBy != "analyst@example.com" {
		t.Errorf("CreatedBy = %q, want the supplied owner email", rec.CreatedBy)
	}
	if rec.SinkClass != memstore.SinkSchemaEntity || rec.Category != memstore.CategoryCorrection {
		t.Errorf("sink/category = %q/%q", rec.SinkClass, rec.Category)
	}
	if rec.Dimension != memstore.DimensionKnowledge {
		t.Errorf("Dimension = %q, want knowledge", rec.Dimension)
	}
	// schema_entity is a reviewed class: the pending-insight overlay must be set
	// so apply_knowledge surfaces it for review before promotion.
	if rec.Metadata[memstore.MetaKeyInsightStatus] != memstore.InsightStatusPending {
		t.Errorf("expected pending insight overlay, metadata=%v", rec.Metadata)
	}
	if rec.Metadata[memstore.MetaKeySessionID] != "sess-abc" {
		t.Errorf("expected session id in overlay, metadata=%v", rec.Metadata)
	}
	if rec.Metadata["reflexive_trigger"] != "query_error_fix" {
		t.Errorf("expected caller metadata preserved, metadata=%v", rec.Metadata)
	}
	if res.Status != memstore.StatusActive {
		t.Errorf("Status = %q, want active", res.Status)
	}
}

func TestAutoCapture_ExplicitSourcePreserved(t *testing.T) {
	store := &mockStore{}
	tk := newTestToolkit(store, nil)

	if _, err := tk.AutoCapture(context.Background(), AutoCaptureInput{
		SinkClass: memstore.SinkBusinessKnowledge,
		Content:   "Stores close at 9pm on weekdays across all regions.",
		Source:    memstore.SourceLineageEvent,
		CreatedBy: "ops@example.com",
	}); err != nil {
		t.Fatalf("AutoCapture: %v", err)
	}
	if got := store.insertedRecords[0].Source; got != memstore.SourceLineageEvent {
		t.Errorf("Source = %q, want lineage_event (explicit source preserved)", got)
	}
}

func TestAutoCapture_Validation(t *testing.T) {
	tk := newTestToolkit(&mockStore{}, nil)
	tests := []struct {
		name string
		in   AutoCaptureInput
	}{
		{"missing owner", AutoCaptureInput{SinkClass: memstore.SinkSchemaEntity, Content: "long enough content here"}},
		{"bad sink class", AutoCaptureInput{SinkClass: "nonsense", Content: "long enough content here", CreatedBy: "a@b.c"}},
		{"content too short", AutoCaptureInput{SinkClass: memstore.SinkSchemaEntity, Content: "x", CreatedBy: "a@b.c"}},
		{"bad source", AutoCaptureInput{SinkClass: memstore.SinkSchemaEntity, Content: "long enough content here", Source: "wat", CreatedBy: "a@b.c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tk.AutoCapture(context.Background(), tt.in); err == nil {
				t.Errorf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestAutoCapture_RecallFirstSupersedes(t *testing.T) {
	store := &mockStore{}
	tk := newTestToolkit(store, nil)
	tk.SetRecallChecker(&fakeRecallChecker{id: "old-mem", score: 0.95})

	res, err := tk.AutoCapture(context.Background(), AutoCaptureInput{
		SinkClass: memstore.SinkSchemaEntity,
		Content:   "The amount column excludes returns on sales.orders.",
		CreatedBy: "analyst@example.com",
	})
	if err != nil {
		t.Fatalf("AutoCapture: %v", err)
	}
	if res.Superseded != "old-mem" {
		t.Errorf("Superseded = %q, want old-mem (recall-first supersede)", res.Superseded)
	}
	if store.supersededOld != "old-mem" || store.supersededNew != res.ID {
		t.Errorf("supersede call = (%q,%q), want (old-mem,%q)", store.supersededOld, store.supersededNew, res.ID)
	}
}

func TestAutoCapture_InsertErrorPropagates(t *testing.T) {
	store := &mockStore{insertErr: errBoom}
	tk := newTestToolkit(store, nil)
	if _, err := tk.AutoCapture(context.Background(), AutoCaptureInput{
		SinkClass: memstore.SinkSchemaEntity,
		Content:   "long enough content here for validation",
		CreatedBy: "a@b.c",
	}); err == nil {
		t.Error("expected insert error to propagate")
	}
}
