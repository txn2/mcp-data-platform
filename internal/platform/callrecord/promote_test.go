package callrecord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// promoteStore is a catalog holding one record, which the promoter reads,
// updates, and reads back.
type promoteStore struct {
	Store
	rec       *Record
	promotion *Promotion
	rejection *Rejection
	getErr    error
}

func (p *promoteStore) Get(context.Context, Scope) (*Record, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	copied := *p.rec
	return &copied, nil
}

func (p *promoteStore) Promote(_ context.Context, _ string, pr Promotion) error {
	p.promotion = &pr
	p.rec.PromotedURN = pr.URN
	p.rec.PromotedBy = pr.Actor
	return nil
}

func (p *promoteStore) Reject(_ context.Context, _ string, r Rejection) error {
	p.rejection = &r
	at := time.Now()
	p.rec.RejectedAt = &at
	p.rec.RejectedBy = r.Actor
	p.rec.RejectionNote = r.Note
	return nil
}

// queryWriter stands in for the DataHub curated-query write path.
type queryWriter struct {
	datasetURNs []string
	name        string
	sql         string
	description string
	err         error
}

func (q *queryWriter) CreateCuratedQuery(_ context.Context, datasetURNs []string, name, sql, description string) (string, error) {
	q.datasetURNs, q.name, q.sql, q.description = datasetURNs, name, sql, description
	if q.err != nil {
		return "", q.err
	}
	return "urn:li:query:abc", nil
}

// exampleWriter stands in for the API catalog's example store.
type exampleWriter struct {
	saved Example
}

func (e *exampleWriter) SaveExample(_ context.Context, ex Example) (string, error) {
	e.saved = ex
	return "example-1", nil
}

func satisfiedSQL() *Record {
	return &Record{
		ID:         "call-1",
		Kind:       KindSQL,
		Outcome:    OutcomeSatisfied,
		Purpose:    "Revenue by region for the board deck.",
		Statement:  "SELECT region, SUM(amount) FROM sales.orders GROUP BY region",
		SessionID:  "dps_abc",
		ReuseCount: 2,
		Targets: []string{
			"urn:li:dataset:(urn:li:dataPlatform:trino,sales.orders,PROD)",
			"urn:li:dataset:(urn:li:dataPlatform:trino,sales.regions,PROD)",
		},
	}
}

func TestPromoteQueryCarriesEveryDatasetItReads(t *testing.T) {
	t.Parallel()

	store := &promoteStore{rec: satisfiedSQL()}
	writer := &queryWriter{}
	promoter := NewPromoter(store, writer, nil)

	got, err := promoter.Promote(context.Background(), Scope{ID: "call-1"}, "reviewer@example.com")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// A query that joins two tables belongs to both, and DataHub's own write
	// accepts the set.
	if len(writer.datasetURNs) != 2 {
		t.Errorf("dataset URNs = %v, want both targets", writer.datasetURNs)
	}
	if writer.name != "Revenue by region for the board deck." {
		t.Errorf("name = %q, want the stated purpose", writer.name)
	}
	if !strings.Contains(writer.description, "dps_abc") {
		t.Errorf("description %q must lead back to the session that produced it", writer.description)
	}
	if !strings.Contains(writer.description, "2 later session") {
		t.Errorf("description %q must carry the reuse a reviewer weighed", writer.description)
	}
	if got.PromotedURN != "urn:li:query:abc" || store.promotion.Actor != "reviewer@example.com" {
		t.Errorf("promotion not recorded on the record: %+v", got)
	}
}

func TestPromoteAPICallBecomesAnExample(t *testing.T) {
	t.Parallel()

	store := &promoteStore{rec: &Record{
		ID: "call-2", Kind: KindAPI, Outcome: OutcomeSatisfied,
		Purpose: "Listing open orders.", Connection: "acme-crm",
		Method: "GET", Path: "/v1/orders", OperationID: "listOrders",
	}}
	examples := &exampleWriter{}
	promoter := NewPromoter(store, nil, examples)

	if _, err := promoter.Promote(context.Background(), Scope{ID: "call-2"}, "reviewer@example.com"); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if examples.saved.OperationID != "listOrders" || examples.saved.Connection != "acme-crm" {
		t.Errorf("example not saved against its endpoint: %+v", examples.saved)
	}
	if examples.saved.CallRecordID != "call-2" {
		t.Error("an example must lead back to the call it was promoted from")
	}
}

func TestPromoteRefusesARecordThatAnsweredNothing(t *testing.T) {
	t.Parallel()

	store := &promoteStore{rec: &Record{ID: "call-3", Kind: KindSQL, Outcome: OutcomeRan}}
	promoter := NewPromoter(store, &queryWriter{}, nil)

	_, err := promoter.Promote(context.Background(), Scope{ID: "call-3"}, "reviewer@example.com")
	if !errors.Is(err, ErrNotPromotable) {
		t.Errorf("err = %v, want ErrNotPromotable", err)
	}
}

func TestPromoteRefusesWithNowhereToPromoteTo(t *testing.T) {
	t.Parallel()

	// A deployment with no DataHub must refuse rather than report a promotion
	// that persisted nothing.
	store := &promoteStore{rec: satisfiedSQL()}
	promoter := NewPromoter(store, nil, nil)

	_, err := promoter.Promote(context.Background(), Scope{ID: "call-1"}, "reviewer@example.com")
	if !errors.Is(err, ErrNoPromotionTarget) {
		t.Errorf("err = %v, want ErrNoPromotionTarget", err)
	}
	if store.promotion != nil {
		t.Error("a refused promotion must not be recorded on the record")
	}
}

func TestPromoteLeavesTheRecordAloneWhenTheWriteFails(t *testing.T) {
	t.Parallel()

	store := &promoteStore{rec: satisfiedSQL()}
	promoter := NewPromoter(store, &queryWriter{err: errors.New("datahub down")}, nil)

	if _, err := promoter.Promote(context.Background(), Scope{ID: "call-1"}, "reviewer@example.com"); err == nil {
		t.Fatal("expected the write failure to surface")
	}
	if store.promotion != nil {
		t.Error("a record must not be marked promoted when nothing was written")
	}
}

func TestPromoteRefusesAStatementItNoLongerHas(t *testing.T) {
	t.Parallel()

	// Audit parameter policy can drop a statement. Promoting the record would
	// create a catalog query with no query in it.
	rec := satisfiedSQL()
	rec.Statement = ""
	promoter := NewPromoter(&promoteStore{rec: rec}, &queryWriter{}, nil)

	_, err := promoter.Promote(context.Background(), Scope{ID: "call-1"}, "reviewer@example.com")
	if !errors.Is(err, ErrNotPromotable) {
		t.Errorf("err = %v, want ErrNotPromotable", err)
	}
}

func TestRejectRecordsTheDecision(t *testing.T) {
	t.Parallel()

	store := &promoteStore{rec: satisfiedSQL()}
	promoter := NewPromoter(store, &queryWriter{}, nil)

	got, err := promoter.Reject(context.Background(), Scope{ID: "call-1"}, "reviewer@example.com", "Superseded by the revenue view.")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if store.rejection == nil || store.rejection.Note == "" {
		t.Fatalf("rejection not recorded: %+v", store.rejection)
	}
	// A declined record must stop being offered, which is what the queue reads.
	if got.Promotable() {
		t.Error("a declined record must not still be promotable")
	}
}

func TestPromotedNameFallsBackToWhatWasAddressed(t *testing.T) {
	t.Parallel()

	if got := PromotedName(Record{Purpose: "  Revenue.  "}); got != "Revenue." {
		t.Errorf("name = %q, want the trimmed purpose", got)
	}
	if got := PromotedName(Record{OperationID: "listOrders"}); got != "listOrders" {
		t.Errorf("name = %q, want the operation id", got)
	}
	if got := PromotedName(Record{Method: "GET", Path: "/v1/orders"}); got != "GET /v1/orders" {
		t.Errorf("name = %q, want the request line", got)
	}
	if got := PromotedName(Record{ToolName: "trino_query", Connection: "acme"}); got != "trino_query on acme" {
		t.Errorf("name = %q, want the tool and connection", got)
	}
	// A catalog list is unreadable when an entry is a paragraph.
	long := PromotedName(Record{Purpose: strings.Repeat("a", 400)})
	if len([]rune(long)) != maxPromotedNameLen {
		t.Errorf("a long purpose must be cut to %d runes, got %d", maxPromotedNameLen, len([]rune(long)))
	}
}

func TestPromoteReportsAnUnreadableRecord(t *testing.T) {
	t.Parallel()

	promoter := NewPromoter(&promoteStore{getErr: ErrNotFound}, &queryWriter{}, nil)
	if _, err := promoter.Promote(context.Background(), Scope{ID: "gone"}, "reviewer@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := promoter.Reject(context.Background(), Scope{ID: "gone"}, "reviewer@example.com", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
