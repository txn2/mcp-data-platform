package compactor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
)

// windowColumns are the columns of a compacted window's Parquet file: the event
// columns, without dt, hour and minute, which the partition carries.
var windowColumns = fileColumns()

// fileColumns builds windowColumns. Every type it names is a constant the type
// parser reads, so a failure is a programming error, reported at start-up.
func fileColumns() []tabletype.Column {
	cols := make([]tabletype.Column, 0, len(whevent.Columns))
	for _, name := range whevent.Columns {
		text := "varchar"
		if name == "received_at" || name == "landed_at" {
			text = "timestamp(6)"
		}
		typ, err := tabletype.Parse(text)
		if err != nil {
			panic(fmt.Sprintf("compactor: column %s: %v", name, err))
		}
		cols = append(cols, tabletype.Column{Name: name, Type: typ})
	}
	return cols
}

// encodeWindow renders a window's events as a Parquet file, in whevent.Columns
// order.
func encodeWindow(events []whevent.Stored) ([]byte, error) {
	rows := make([][]any, 0, len(events))
	for _, e := range events {
		rows = append(rows, []any{
			e.ReceivedAt.UTC(), e.LandedAt.UTC(), e.EventID, e.EventType, e.Key,
			e.ContentHash, e.Replica, string(e.Payload),
		})
	}
	data, err := tableparquet.Write(windowColumns, rows)
	if err != nil {
		return nil, fmt.Errorf("writing the window's Parquet file: %w", err)
	}
	return data, nil
}

// decodeWindow reads a Parquet file encodeWindow wrote. It is how a compaction of
// a window whose raw segments retention already deleted keeps the events those
// segments held.
func decodeWindow(data []byte) ([]whevent.Stored, error) {
	f, err := parquet.OpenFile(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening the window's Parquet file: %w", err)
	}
	index := make(map[string]int, len(whevent.Columns))
	for _, name := range whevent.Columns {
		leaf, ok := f.Schema().Lookup(name)
		if !ok {
			return nil, fmt.Errorf("the window's Parquet file has no column %s", name)
		}
		index[name] = leaf.ColumnIndex
	}
	var out []whevent.Stored
	for _, rg := range f.RowGroups() {
		rows, err := readRows(rg, index)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// readRows reads one row group.
func readRows(rg parquet.RowGroup, index map[string]int) ([]whevent.Stored, error) {
	reader := rg.Rows()
	defer func() { _ = reader.Close() }()
	var out []whevent.Stored
	buf := make([]parquet.Row, 256)
	for {
		n, err := reader.ReadRows(buf)
		for _, row := range buf[:n] {
			out = append(out, storedOf(row, index))
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("reading the window's Parquet file: %w", err)
		}
		if n == 0 {
			return out, nil
		}
	}
}

// storedOf maps one row's values to an event.
func storedOf(row parquet.Row, index map[string]int) whevent.Stored {
	byCol := make(map[int]parquet.Value, len(row))
	for _, v := range row {
		byCol[v.Column()] = v
	}
	str := func(name string) string {
		v, ok := byCol[index[name]]
		if !ok || v.IsNull() {
			return ""
		}
		return string(v.ByteArray())
	}
	ts := func(name string) time.Time {
		v, ok := byCol[index[name]]
		if !ok || v.IsNull() {
			return time.Time{}
		}
		return time.UnixMicro(v.Int64()).UTC()
	}
	return whevent.Stored{
		Event: whevent.Event{
			ReceivedAt: ts("received_at"), EventID: str("event_id"), EventType: str("event_type"),
			Key: str("key"), ContentHash: str("content_hash"), Replica: str("replica"),
			Payload: []byte(str("payload")),
		},
		LandedAt: ts("landed_at"),
	}
}
