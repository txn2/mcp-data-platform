package whevent

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// TimestampLayout is how a timestamp is written into a segment: the text the
// Hive JSON reader parses as a TIMESTAMP(6), in UTC.
const TimestampLayout = "2006-01-02 15:04:05.000000"

// Columns are the columns every source's tables and view declare, in order.
// dt, hour and minute are the partition columns and are not written into a file.
var Columns = []string{
	"received_at", "landed_at", "event_id", "event_type", "key",
	"content_hash", "replica", "payload",
}

// line is one event as a segment holds it.
type line struct {
	ReceivedAt  string `json:"received_at"`
	LandedAt    string `json:"landed_at"`
	EventID     string `json:"event_id"`
	EventType   string `json:"event_type"`
	Key         string `json:"key"`
	ContentHash string `json:"content_hash"`
	Replica     string `json:"replica"`
	Payload     string `json:"payload"`
}

// Stored is one event read back from a segment, with the time the segment
// holding it was written.
type Stored struct {
	Event
	LandedAt time.Time
}

// EncodeSegment renders events as gzipped JSON-lines, each stamped with
// landedAt.
func EncodeSegment(events []Event, landedAt time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	landed := landedAt.UTC().Format(TimestampLayout)
	for _, e := range events {
		if err := enc.Encode(line{
			ReceivedAt:  e.ReceivedAt.UTC().Format(TimestampLayout),
			LandedAt:    landed,
			EventID:     e.EventID,
			EventType:   e.EventType,
			Key:         e.Key,
			ContentHash: e.ContentHash,
			Replica:     e.Replica,
			Payload:     string(e.Payload),
		}); err != nil {
			return nil, fmt.Errorf("encoding segment line: %w", err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing segment: %w", err)
	}
	return buf.Bytes(), nil
}

// maxLineBytes bounds one line of a segment when it is read back. A line is
// one event, and an event is at most one body, which a source bounds far
// below this.
const maxLineBytes = 256 << 20

// DecodeSegment reads a segment written by EncodeSegment.
func DecodeSegment(data []byte) ([]Stored, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("opening segment: %w", err)
	}
	defer func() { _ = zr.Close() }()
	sc := bufio.NewScanner(io.LimitReader(zr, 1<<34))
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	var out []Stored
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("decoding segment line: %w", err)
		}
		st, err := l.stored()
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading segment: %w", err)
	}
	return out, nil
}

// stored parses a line's timestamps.
func (l line) stored() (Stored, error) {
	received, err := time.ParseInLocation(TimestampLayout, l.ReceivedAt, time.UTC)
	if err != nil {
		return Stored{}, fmt.Errorf("segment received_at %q: %w", l.ReceivedAt, err)
	}
	landed, err := time.ParseInLocation(TimestampLayout, l.LandedAt, time.UTC)
	if err != nil {
		return Stored{}, fmt.Errorf("segment landed_at %q: %w", l.LandedAt, err)
	}
	return Stored{
		Event: Event{
			ReceivedAt: received, EventID: l.EventID, EventType: l.EventType, Key: l.Key,
			ContentHash: l.ContentHash, Replica: l.Replica, Payload: []byte(l.Payload),
		},
		LandedAt: landed,
	}, nil
}
