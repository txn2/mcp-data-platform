package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopStore_Insert(t *testing.T) {
	store := NewNoopStore()
	err := store.Insert(context.Background(), Insight{
		ID:          "test-id",
		Category:    "correction",
		InsightText: "This is a test insight",
		Status:      "pending",
	})
	assert.NoError(t, err)
}

func TestPostgresStore_Insert(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := NewPostgresStore(db)
	insight := Insight{
		ID:          "insight-1",
		SessionID:   "sess-1",
		CapturedBy:  "user-1",
		Persona:     "analyst",
		Category:    "correction",
		InsightText: "Column name is misleading",
		Confidence:  "high",
		EntityURNs:  []string{"urn:li:dataset:foo"},
		RelatedColumns: []RelatedColumn{
			{URN: "urn:li:dataset:foo", Column: "col1", Relevance: "primary"},
		},
		SuggestedActions: []SuggestedAction{
			{ActionType: "add_tag", Target: "urn:li:dataset:foo", Detail: "misleading"},
		},
		Status: "pending",
	}

	mock.ExpectExec("INSERT INTO knowledge_insights").
		WithArgs(
			insight.ID, insight.SessionID, insight.CapturedBy, insight.Persona,
			insight.Category, insight.InsightText, insight.Confidence,
			sqlmock.AnyArg(), // entity_urns JSON
			sqlmock.AnyArg(), // related_columns JSON
			sqlmock.AnyArg(), // suggested_actions JSON
			insight.Status,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Insert(context.Background(), insight)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Insert_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // sqlmock db close error is inconsequential in tests.

	store := NewPostgresStore(db)
	insight := Insight{
		ID:               "insight-2",
		Category:         "data_quality",
		InsightText:      "Timestamps are wrong",
		Confidence:       "medium",
		EntityURNs:       []string{},
		RelatedColumns:   []RelatedColumn{},
		SuggestedActions: []SuggestedAction{},
		Status:           "pending",
	}

	mock.ExpectExec("INSERT INTO knowledge_insights").
		WithArgs(
			insight.ID, insight.SessionID, insight.CapturedBy, insight.Persona,
			insight.Category, insight.InsightText, insight.Confidence,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			insight.Status,
		).
		WillReturnError(errors.New("connection refused"))

	err = store.Insert(context.Background(), insight)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "inserting insight")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// spyStore is a test helper that captures inserted insights.
type spyStore struct {
	Insights []Insight
	Err      error
}

func (s *spyStore) Insert(_ context.Context, insight Insight) error {
	if s.Err != nil {
		return s.Err
	}
	s.Insights = append(s.Insights, insight)
	return nil
}
