package sources

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataHubSource_NameAndOperations(t *testing.T) {
	s := NewDataHubSource(nil, nil)
	assert.Equal(t, "datahub", s.Name())
	assert.ElementsMatch(t, []string{"get_entity", "get_glossary_term"}, s.Operations())
}

func TestDataHubSource_ExecuteRejectsUnknownOp(t *testing.T) {
	s := NewDataHubSource(nil, nil)
	_, err := s.Execute(context.Background(), "delete_entity", map[string]any{"urn": "x"})
	assert.ErrorContains(t, err, "not supported")
}

func TestDataHubSource_GetEntitySuccess(t *testing.T) {
	got := ""
	fn := func(_ context.Context, urn string) (any, error) {
		got = urn
		return map[string]any{"urn": urn, "owners": []string{"alice"}}, nil
	}
	s := NewDataHubSource(fn, nil)
	res, err := s.Execute(context.Background(), "get_entity", map[string]any{"urn": "urn:li:dataset:1"})
	require.NoError(t, err)
	assert.Equal(t, "urn:li:dataset:1", got)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "urn:li:dataset:1", m["urn"])
}

func TestDataHubSource_GetGlossaryTermSuccess(t *testing.T) {
	fn := func(_ context.Context, urn string) (any, error) {
		return map[string]any{"urn": urn, "name": "PII"}, nil
	}
	s := NewDataHubSource(nil, fn)
	res, err := s.Execute(context.Background(), "get_glossary_term", map[string]any{"urn": "urn:li:glossaryTerm:PII"})
	require.NoError(t, err)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "PII", m["name"])
}

func TestDataHubSource_NotConfigured(t *testing.T) {
	s := NewDataHubSource(nil, nil)
	_, err := s.Execute(context.Background(), "get_entity", map[string]any{"urn": "x"})
	assert.ErrorContains(t, err, "not configured")
}

func TestDataHubSource_RequiresURN(t *testing.T) {
	fn := func(context.Context, string) (any, error) { return map[string]any{}, nil }
	s := NewDataHubSource(fn, fn)
	_, err := s.Execute(context.Background(), "get_entity", map[string]any{})
	assert.ErrorContains(t, err, "urn")
}

func TestDataHubSource_PropagatesUpstreamError(t *testing.T) {
	fn := func(context.Context, string) (any, error) {
		return nil, errors.New("404 not found")
	}
	s := NewDataHubSource(fn, nil)
	_, err := s.Execute(context.Background(), "get_entity", map[string]any{"urn": "urn:li:dataset:missing"})
	assert.ErrorContains(t, err, "404 not found")
}
