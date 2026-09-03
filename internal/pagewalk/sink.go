package pagewalk

import "encoding/json"

// InlineMerge is api_invoke_endpoint's sink: the merged array held in
// memory under a byte limit. A page that would pass the limit is
// refused whole, so the result is always a prefix of the collection at
// a page boundary, and the walk reports where it stopped.
type InlineMerge struct {
	Limit int64
	// Rendered measures the merged array as the caller's tool result
	// will render it. The limit is expressed against that rather than
	// against the compact bytes the pages hold, because what a client
	// accepts is the rendered result and the indentation between the two
	// is several times the compact size (issue #1606). nil measures the
	// compact bytes.
	Rendered func(items []json.RawMessage) int64
	items    []json.RawMessage
	size     int64
}

// Add is the Sink.
func (m *InlineMerge) Add(items []json.RawMessage) error {
	var pageSize int64
	for _, it := range items {
		pageSize += int64(len(it)) + 1
	}
	if m.Limit > 0 && m.sizeWith(items, pageSize) > m.Limit {
		return ErrPageDoesNotFit
	}
	m.items = append(m.items, items...)
	m.size += pageSize
	return nil
}

// sizeWith is the size the merge would hold were the page added,
// measured the way the limit is expressed.
func (m *InlineMerge) sizeWith(items []json.RawMessage, pageSize int64) int64 {
	if m.Rendered == nil {
		return m.size + pageSize
	}
	merged := make([]json.RawMessage, 0, len(m.items)+len(items))
	merged = append(merged, m.items...)
	merged = append(merged, items...)
	return m.Rendered(merged)
}

// Size is the bytes the merged array holds: what the call returns.
func (m *InlineMerge) Size() int64 { return m.size }

// Merged returns the array to report. An empty walk is an empty array,
// not null: the caller asked for a collection.
func (m *InlineMerge) Merged() []json.RawMessage {
	if m.items == nil {
		return []json.RawMessage{}
	}
	return m.items
}
