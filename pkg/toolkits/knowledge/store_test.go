package knowledge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
