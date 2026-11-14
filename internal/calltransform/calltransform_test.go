package calltransform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// masker masks a "card" key in an object or in each item of a list, adds a
// row, upper-cases a text body, and returns page when told to.
type masker struct {
	err  error
	page any
}

func (m masker) Rows(_ context.Context, _ []string, rows []map[string]any) ([]map[string]any, error) {
	return append(rows, map[string]any{"added": true}), m.err
}

func (m masker) Body(_ context.Context, body any) (any, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.page != nil {
		return m.page, nil
	}
	switch v := body.(type) {
	case map[string]any:
		v["card"] = "****"
	case []any:
		for _, it := range v {
			if o, ok := it.(map[string]any); ok {
				o["card"] = "****"
			}
		}
	case string:
		return strings.ToUpper(v), nil
	}
	return body, nil
}

func text(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

// TestContext carries the transforms covering a call, and leaves rows and
// bodies alone when none covers it.
func TestContext(t *testing.T) {
	ctx := context.Background()
	assert.False(t, Covered(ctx))
	rows, err := Rows(ctx, nil, []map[string]any{{"a": 1}})
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	body, err := Body(ctx, "same")
	require.NoError(t, err)
	assert.Equal(t, "same", body)

	ctx = With(ctx, masker{})
	assert.True(t, Covered(ctx))
	rows, err = Rows(ctx, nil, []map[string]any{{"a": 1}})
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	body, err = Body(ctx, "same")
	require.NoError(t, err)
	assert.Equal(t, "SAME", body)
}

// TestExport writes an export body as the covering transform leaves it, JSON
// or text, within the export's cap, and fails rather than write the original.
func TestExport(t *testing.T) {
	ctx := With(context.Background(), masker{})
	got, err := Export(ctx, "api_export", strings.NewReader(`{"card":"4111","id":1}`), 100)
	require.NoError(t, err)
	assert.JSONEq(t, `{"card":"****","id":1}`, text(t, got))
	got, err = Export(ctx, "api_export", strings.NewReader("plain text"), 100)
	require.NoError(t, err)
	assert.Equal(t, "PLAIN TEXT", text(t, got))
	_, err = Export(ctx, "api_export", strings.NewReader(`{"card":"4111"}`), 5)
	require.ErrorContains(t, err, "exceeds api_export cap")
	_, err = Export(With(context.Background(), masker{err: errors.New("timed out")}), "api_export", strings.NewReader(`{}`), 100)
	require.ErrorContains(t, err, "timed out")
	_, err = Export(ctx, "api_export", broken{}, 100)
	require.Error(t, err)
	_, err = Export(With(context.Background(), masker{page: make(chan int)}), "api_export", strings.NewReader(`{}`), 100)
	require.ErrorContains(t, err, "could not be written")
}

type broken struct{}

func (broken) Read([]byte) (int, error) { return 0, errors.New("reset") }

// TestPages passes each page through the transform before it is written, and
// never writes a page short without saying so.
func TestPages(t *testing.T) {
	var written []json.RawMessage
	sink := func(items []json.RawMessage) error { written = items; return nil }
	page := []json.RawMessage{json.RawMessage(`{"card":"4111"}`), json.RawMessage(`{"card":"5500"}`)}
	require.NoError(t, Pages(context.Background(), "api_export", sink)(page))
	assert.Equal(t, page, written, "no transform covers the call")
	require.NoError(t, Pages(With(context.Background(), masker{}), "api_export", sink)(page))
	assert.JSONEq(t, `{"card":"****"}`, string(written[0]))
	require.ErrorContains(t, Pages(With(context.Background(), masker{page: map[string]any{}}), "api_export", sink)(page), "not a list")
	require.ErrorContains(t, Pages(With(context.Background(), masker{err: errors.New("timed out")}), "api_export", sink)(page), "timed out")
	require.ErrorContains(t, Pages(With(context.Background(), masker{page: []any{make(chan int)}}), "api_export", sink)(page), "could not be written")
}

// TestData is a graphql_export's data as the covering transform leaves it.
func TestData(t *testing.T) {
	raw := json.RawMessage(`{"card":"4111","id":1}`)
	got, err := Data(context.Background(), "graphql_export", raw)
	require.NoError(t, err)
	assert.Equal(t, raw, got)
	ctx := With(context.Background(), masker{})
	got, err = Data(ctx, "graphql_export", raw)
	require.NoError(t, err)
	assert.JSONEq(t, `{"card":"****","id":1}`, string(got))
	_, err = Data(ctx, "graphql_export", json.RawMessage(`{`))
	require.ErrorContains(t, err, "reading the result")
	_, err = Data(With(context.Background(), masker{err: errors.New("timed out")}), "graphql_export", raw)
	require.ErrorContains(t, err, "timed out")
	_, err = Data(With(context.Background(), masker{page: make(chan int)}), "graphql_export", raw)
	require.ErrorContains(t, err, "writing the transformed result")
}
