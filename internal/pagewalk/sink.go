package pagewalk

import "encoding/json"

// InlineMerge is api_invoke_endpoint's sink: the merged array held in
// memory under a byte limit. A page that would pass the limit is
// refused whole, so the result is always a prefix of the collection at
// a page boundary, and the walk reports where it stopped.
type InlineMerge struct {
	Limit int64
	items []json.RawMessage
	size  int64
}

// Add is the Sink.
func (m *InlineMerge) Add(items []json.RawMessage) error {
	var pageSize int64
	for _, it := range items {
		pageSize += int64(len(it)) + 1
	}
	if m.Limit > 0 && m.size+pageSize > m.Limit {
		return ErrPageDoesNotFit
	}
	m.items = append(m.items, items...)
	m.size += pageSize
	return nil
}

// Merged returns the array to report. An empty walk is an empty array,
// not null: the caller asked for a collection.
func (m *InlineMerge) Merged() []json.RawMessage {
	if m.items == nil {
		return []json.RawMessage{}
	}
	return m.items
}
