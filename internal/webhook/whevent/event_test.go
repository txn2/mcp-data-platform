package whevent

import (
	"bytes"
	"compress/gzip"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

var received = time.Date(2026, 9, 24, 13, 45, 6, 123456000, time.UTC)

func paths(t *testing.T, c whsource.Config) Paths {
	t.Helper()
	p, err := ParsePaths(c)
	require.NoError(t, err)
	return p
}

func TestBuildWholeBody(t *testing.T) {
	p := paths(t, whsource.Config{EventIDPath: "$.id", EventTypePath: "$.type", KeyPath: "$.contact.email"})
	evs, err := Build([]byte(`{"type":"open","id":42,"contact":{"email":"a@example.com"},"n":1.50}`), p, received, "r1")
	require.NoError(t, err)
	require.Len(t, evs, 1)
	e := evs[0]
	assert.Equal(t, "42", e.EventID)
	assert.Equal(t, "open", e.EventType)
	assert.Equal(t, "a@example.com", e.Key)
	assert.Equal(t, "r1", e.Replica)
	assert.Equal(t, received, e.ReceivedAt)
	assert.JSONEq(t, `{"type":"open","id":42,"contact":{"email":"a@example.com"},"n":1.50}`, string(e.Payload))
	assert.Contains(t, string(e.Payload), `1.50`, "numbers are kept as written")
	assert.Len(t, e.ContentHash, 64)
	assert.Equal(t, time.Date(2026, 9, 24, 13, 0, 0, 0, time.UTC), e.Window(time.Hour))
	assert.Equal(t, time.Date(2026, 9, 24, 13, 45, 0, 0, time.UTC), e.Window(5*time.Minute))
}

func TestBuildHashIDIgnoresKeyOrder(t *testing.T) {
	a, err := Build([]byte(`{"a":1,"b":2}`), Paths{}, received, "r")
	require.NoError(t, err)
	b, err := Build([]byte(`{ "b":2, "a":1 }`), Paths{}, received, "r")
	require.NoError(t, err)
	assert.Equal(t, a[0].EventID, b[0].EventID, "without an id path the id is the hash of the canonical event")
	assert.Equal(t, a[0].ContentHash, a[0].EventID)
}

func TestBuildSplit(t *testing.T) {
	p := paths(t, whsource.Config{Split: "$", EventIDPath: "sg_event_id"})
	evs, err := Build([]byte(`[{"sg_event_id":"x"},{"sg_event_id":"y"},{"other":1}]`), p, received, "r")
	require.NoError(t, err)
	require.Len(t, evs, 3)
	assert.Equal(t, "x", evs[0].EventID)
	assert.Equal(t, "y", evs[1].EventID)
	assert.Equal(t, evs[2].ContentHash, evs[2].EventID, "an element without the id falls back to its hash")

	nested := paths(t, whsource.Config{Split: "$.data.events"})
	evs, err = Build([]byte(`{"data":{"events":[]}}`), nested, received, "r")
	require.NoError(t, err)
	assert.Empty(t, evs)

	_, err = Build([]byte(`{"data":{"events":{}}}`), nested, received, "r")
	assert.ErrorIs(t, err, ErrSplitNotArray)
	_, err = Build([]byte(`{}`), nested, received, "r")
	assert.ErrorIs(t, err, ErrSplitNotArray)
}

func TestBuildRefusesNonJSON(t *testing.T) {
	for _, body := range []string{"", "not json", `{"a":1} {"b":2}`, `{"a":`} {
		_, err := Build([]byte(body), Paths{}, received, "r")
		assert.ErrorIs(t, err, ErrNotJSON, body)
	}
}

func TestJSONBody(t *testing.T) {
	body, err := JSONBody("application/json", []byte(`{"a":1}`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(body))

	form := url.Values{"payload": {`{"a":1}`}, "other": {"x"}}.Encode()
	body, err = JSONBody("application/x-www-form-urlencoded; charset=utf-8", []byte(form))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, string(body))

	_, err = JSONBody("application/x-www-form-urlencoded", []byte("other=x"))
	assert.ErrorIs(t, err, ErrNoPayload)
	_, err = JSONBody("application/x-www-form-urlencoded", []byte("%zz"))
	assert.ErrorIs(t, err, ErrNoPayload)
}

func TestParsePathsReportsBadPath(t *testing.T) {
	_, err := ParsePaths(whsource.Config{KeyPath: "$["})
	assert.Error(t, err)
}

func TestSegmentRoundTrip(t *testing.T) {
	landed := received.Add(time.Second)
	evs, err := Build([]byte(`[{"id":"a","s":"line\nbreak <b>"},{"id":"b"}]`),
		paths(t, whsource.Config{Split: "$", EventIDPath: "id"}), received, "r1")
	require.NoError(t, err)
	data, err := EncodeSegment(evs, landed)
	require.NoError(t, err)

	back, err := DecodeSegment(data)
	require.NoError(t, err)
	require.Len(t, back, 2)
	assert.Equal(t, evs[0].EventID, back[0].EventID)
	assert.Equal(t, evs[0].Payload, back[0].Payload)
	assert.Equal(t, received, back[0].ReceivedAt, "microseconds survive the round trip")
	assert.Equal(t, landed, back[0].LandedAt)
	assert.Equal(t, "r1", back[1].Replica)
}

func TestDecodeSegmentRefuses(t *testing.T) {
	_, err := DecodeSegment([]byte("not gzip"))
	assert.Error(t, err)

	for _, l := range []string{
		`{"received_at":`,
		`{"received_at":"x","landed_at":"2026-09-24 13:00:00.000000"}`,
		`{"received_at":"2026-09-24 13:00:00.000000","landed_at":"x"}`,
	} {
		_, err := DecodeSegment(gz(t, l))
		assert.Error(t, err, l)
	}
	out, err := DecodeSegment(gz(t, "\n\n"))
	require.NoError(t, err)
	assert.Empty(t, out)
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}
