// Package calltransform carries the transforms covering a tool call (#2634)
// to the paths where rows or a body reach the caller other than the tool
// result: the pages an export writes and the rows a script reads from an
// application database. The tool-call handlers put a Transformer on the call's
// context; each toolkit asks it where it produces rows or a body, so a covered
// caller gets the same data on every path.
package calltransform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type ctxKey struct{}

// Transformer applies the transforms covering a call.
type Transformer interface {
	// Rows is the rows a covered caller gets, columns naming their keys.
	Rows(ctx context.Context, columns []string, rows []map[string]any) ([]map[string]any, error)
	// Body is the body a covered caller gets.
	Body(ctx context.Context, body any) (any, error)
}

// With carries the transforms covering a call to the toolkit serving it.
func With(ctx context.Context, t Transformer) context.Context {
	return context.WithValue(ctx, ctxKey{}, t)
}

func of(ctx context.Context) Transformer {
	t, _ := ctx.Value(ctxKey{}).(Transformer)
	return t
}

// Covered reports whether a transform covers ctx's call.
func Covered(ctx context.Context) bool { return of(ctx) != nil }

// Rows is rows as the transforms covering ctx's call leave them, or rows as
// they are when none covers it.
func Rows(ctx context.Context, columns []string, rows []map[string]any) ([]map[string]any, error) {
	if t := of(ctx); t != nil {
		return t.Rows(ctx, columns, rows) //nolint:wrapcheck // the transform's own words name it
	}
	return rows, nil
}

// Body is body as the transforms covering ctx's call leave it, or body as it
// is when none covers it.
func Body(ctx context.Context, body any) (any, error) {
	if t := of(ctx); t != nil {
		return t.Body(ctx, body) //nolint:wrapcheck // the transform's own words name it
	}
	return body, nil
}

// Export is an export's body as the transforms covering the call leave it,
// for tool (which names the export in an error), or body as it is when none
// covers it. A covered body is read whole, at most maxBytes, decoded as JSON,
// transformed and written back; one that is not JSON is handed over as text.
func Export(ctx context.Context, tool string, body io.Reader, maxBytes int64) (io.Reader, error) {
	if !Covered(ctx) {
		return body, nil
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading upstream response: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("upstream response exceeds %s cap of %d bytes", tool, maxBytes)
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		decoded = string(raw)
	}
	got, err := Body(ctx, decoded)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", tool, err)
	}
	if s, ok := got.(string); ok {
		return bytes.NewReader([]byte(s)), nil
	}
	out, err := json.Marshal(got)
	if err != nil {
		return nil, fmt.Errorf("in %s: the transformed body could not be written", tool)
	}
	return bytes.NewReader(out), nil
}

// Pages passes each page of items an export merges through the transforms
// covering the call before sink writes it; with none, sink as it is.
func Pages(ctx context.Context, tool string, sink func([]json.RawMessage) error) func([]json.RawMessage) error {
	if !Covered(ctx) {
		return sink
	}
	return func(items []json.RawMessage) error {
		list := make([]any, 0, len(items))
		for _, it := range items {
			var v any
			if json.Unmarshal(it, &v) == nil {
				list = append(list, v)
			}
		}
		got, err := Body(ctx, list)
		if err != nil {
			return fmt.Errorf("in %s: %w", tool, err)
		}
		back, ok := got.([]any)
		if !ok {
			return fmt.Errorf("in %s: a transform returned a page that is not a list of items", tool)
		}
		out := make([]json.RawMessage, 0, len(back))
		for _, v := range back {
			raw, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("in %s: a transformed page could not be written", tool)
			}
			out = append(out, raw)
		}
		return sink(out)
	}
}

// Data is an export's JSON data as the transforms covering the call leave it;
// with none, or no data, as it is.
func Data(ctx context.Context, tool string, data json.RawMessage) (json.RawMessage, error) {
	if !Covered(ctx) || len(data) == 0 {
		return data, nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("in %s: reading the result for its transforms: %w", tool, err)
	}
	got, err := Body(ctx, decoded)
	if err != nil {
		return nil, fmt.Errorf("in %s: %w", tool, err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		return nil, errors.New(tool + ": writing the transformed result failed")
	}
	return out, nil
}
