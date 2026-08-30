package resource

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestIndexText(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "every field composed in order",
			got: IndexText(Resource{
				DisplayName: "Sales Dictionary", Description: "Column reference", Path: "references",
				Filename: "sales.csv", Tags: []string{"finance", "canonical"},
			}, "order_id,net_amount"),
			want: "Sales Dictionary\nColumn reference\nreferences\nsales.csv\nfinance canonical\norder_id,net_amount",
		},
		{
			name: "empty fields are skipped rather than padded",
			got:  IndexText(Resource{DisplayName: "Only Name"}, ""),
			want: "Only Name",
		},
		{
			name: "content is included so a body-only term is indexed",
			got:  IndexText(Resource{DisplayName: "R"}, "gross_margin_pct"),
			want: "R\ngross_margin_pct",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("IndexText = %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestSearchQueryEffectiveLimit(t *testing.T) {
	tests := []struct {
		limit int
		want  int
	}{
		{0, DefaultSearchLimit},
		{-5, DefaultSearchLimit},
		{7, 7},
		{maxSearchLimit + 1, DefaultSearchLimit},
	}
	for _, tt := range tests {
		if got := (SearchQuery{Limit: tt.limit}).EffectiveLimit(); got != tt.want {
			t.Errorf("EffectiveLimit(%d) = %d, want %d", tt.limit, got, tt.want)
		}
	}
}

func TestFuseHybridScore(t *testing.T) {
	// A lexical match must outrank a merely semantically-near row.
	nearMiss := fuseHybridScore(0.9, false)
	exact := fuseHybridScore(0.5, true)
	if exact <= nearMiss {
		t.Errorf("lexical match (%f) should outrank a semantic near-miss (%f)", exact, nearMiss)
	}
	if got := fuseHybridScore(1, true); got != 1 {
		t.Errorf("perfect match = %f, want 1", got)
	}
	if got := fuseHybridScore(-1, false); got != 0 {
		t.Errorf("worst case = %f, want 0", got)
	}
}

func TestScopeVisibilityWhere(t *testing.T) {
	where, args, next := scopeVisibilityWhere([]ScopeFilter{
		{Scope: ScopeGlobal},
		{Scope: ScopePersona, ScopeID: "analyst"},
	}, 3)
	if where != "((scope = $3 AND scope_id IS NULL) OR (scope = $4 AND scope_id = $5))" {
		t.Errorf("where = %q", where)
	}
	if len(args) != 3 || args[0] != "global" || args[1] != "persona" || args[2] != "analyst" {
		t.Errorf("args = %v", args)
	}
	if next != 6 {
		t.Errorf("next = %d, want 6", next)
	}
}

// searchRows builds a result set in the ranked-search projection: the resource
// columns plus the two score columns each hybrid arm appends.
func searchRows(extraCols []string) *sqlmock.Rows {
	return sqlmock.NewRows(append([]string{
		"id", "scope", "scope_id", "path", "filename", "display_name", "description",
		"mime_type", "size_bytes", "s3_key", "uri", "tags", "uploader_sub", "uploader_email",
		"created_at", "updated_at", "last_read_at",
		"thumbnail_s3_key", "thumbnail_dark_s3_key",
		"thumbnail_captured_at", "thumbnail_dark_captured_at",
	}, extraCols...))
}

func addSearchRow(rows *sqlmock.Rows, id, displayName string, extra ...driver.Value) *sqlmock.Rows {
	now := time.Now()
	base := []driver.Value{
		id, "global", nil, "references", "d.csv", displayName, "desc",
		"text/csv", int64(10), "k", "mcp://global/references/" + id, pq.Array([]string{"t"}),
		"sub", "u@example.com", now, now, nil, "", "", nil, nil,
	}
	vals := make([]driver.Value, 0, len(base)+len(extra))
	vals = append(vals, base...)
	return rows.AddRow(append(vals, extra...)...)
}

func TestSearch_EmptyScopesFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	got, err := (&postgresStore{db: db}).Search(context.Background(), SearchQuery{QueryText: "anything"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got != nil {
		t.Errorf("expected no results for a caller with no visible scopes, got %d", len(got))
	}
	// The important half: no SQL ran at all, so an empty scope set can never
	// degrade into an unscoped query.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSearch_LexicalArm(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows := addSearchRow(searchRows([]string{"lex_rank"}), "res_1", "Sales Dictionary", 0.42)
	mock.ExpectQuery("SELECT .+ FROM resources WHERE resource_fts").
		WithArgs("net amount", "global", "user", "sub-1").
		WillReturnRows(rows)

	got, err := (&postgresStore{db: db}).Search(context.Background(), SearchQuery{
		QueryText: "net amount",
		Scopes:    []ScopeFilter{{Scope: ScopeGlobal}, {Scope: ScopeUser, ScopeID: "sub-1"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Resource.ID != "res_1" || got[0].Score != 0.42 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Resource.Tags == nil {
		t.Error("tags should be non-nil after scan")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSearch_HybridArmsDedupeKeepingHigherScore(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	// The same resource matched by both arms: the vector arm without a lexical
	// match, the lexical arm with one. The fused result keeps the higher score.
	rows := searchRows([]string{"vec_score", "lex_match"})
	addSearchRow(rows, "res_1", "Sales Dictionary", 0.8, false)
	addSearchRow(rows, "res_1", "Sales Dictionary", 0.8, true)
	addSearchRow(rows, "res_2", "Other", 0.1, false)
	mock.ExpectQuery("SELECT .+ FROM resources WHERE embedding IS NOT NULL").WillReturnRows(rows)

	got, err := (&postgresStore{db: db}).Search(context.Background(), SearchQuery{
		Embedding: []float32{0.1, 0.2},
		QueryText: "net amount",
		Scopes:    []ScopeFilter{{Scope: ScopeGlobal}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped results, got %d: %+v", len(got), got)
	}
	if got[0].Resource.ID != "res_1" {
		t.Errorf("expected the lexical match first, got %+v", got[0])
	}
	if want := fuseHybridScore(0.8, true); got[0].Score != want {
		t.Errorf("score = %f, want the higher fused score %f", got[0].Score, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestSearch_QueryErrorsAreReported(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT .+ FROM resources").WillReturnError(errors.New("boom"))
	if _, err := (&postgresStore{db: db}).Search(context.Background(), SearchQuery{
		QueryText: "q", Scopes: []ScopeFilter{{Scope: ScopeGlobal}},
	}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected the store error to surface, got %v", err)
	}
}

// A metadata edit must invalidate the stored vector, or the resource keeps
// ranking on its pre-edit text forever.
func TestUpdate_ClearsEmbeddingForReindex(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectExec("UPDATE resources SET updated_at = \\$1, embedding = NULL, embedding_model = '', embedding_text_hash = NULL, description = \\$2 WHERE id = \\$3").
		WillReturnResult(sqlmock.NewResult(0, 1))

	desc := "new description"
	if err := NewPostgresStore(db).Update(context.Background(), "res_1", Update{Description: &desc}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestIsObjectNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"nosuchkey", errors.New("s3 get: NoSuchKey: the specified key does not exist"), true},
		{"status 404", errors.New("s3 get: api error, status code: 404"), true},
		{"plain not found", errors.New("not found"), true},
		{"connection reset", errors.New("connection reset by peer"), false},
		{"permission denied", errors.New("s3 get: AccessDenied: access denied"), false},
		{"timeout", errors.New("context deadline exceeded"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsObjectNotFound(tt.err); got != tt.want {
				t.Errorf("IsObjectNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
