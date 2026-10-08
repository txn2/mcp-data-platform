package exportstream

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestJSONArrayWriter(t *testing.T) {
	var sb strings.Builder
	a := NewJSONArrayWriter(&sb)
	if err := a.Write([]json.RawMessage{json.RawMessage("1"), json.RawMessage(`{"a":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := a.Write([]json.RawMessage{json.RawMessage("3")}); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil || sb.String() != `[1,{"a":2},3]` {
		t.Errorf("got %q err %v", sb.String(), err)
	}
	var empty strings.Builder
	if err := NewJSONArrayWriter(&empty).Close(); err != nil || empty.String() != "[]" {
		t.Errorf("empty walk = %q err %v", empty.String(), err)
	}
	pr, pw := io.Pipe()
	_ = pr.Close()
	b := NewJSONArrayWriter(pw)
	if err := b.Write([]json.RawMessage{json.RawMessage("1")}); !errors.Is(err, ErrConsumerStopped) {
		t.Errorf("closed-pipe write classified as %v", err)
	}
	if err := NewJSONArrayWriter(pw).Close(); !errors.Is(err, ErrConsumerStopped) {
		t.Errorf("closed-pipe close classified as %v", err)
	}
	if err := consumerError(errors.New("disk full")); err == nil || errors.Is(err, ErrConsumerStopped) {
		t.Errorf("a storage error classified as the consumer stopping: %v", err)
	}
}

func TestCappedReader(t *testing.T) {
	under := NewCappedReader(strings.NewReader("abcd"), 4)
	if b, err := io.ReadAll(under); err != nil || string(b) != "abcd" || under.Exceeded() {
		t.Errorf("at the cap: %q %v exceeded=%v", b, err, under.Exceeded())
	}
	over := NewCappedReader(strings.NewReader("abcde"), 4)
	if _, err := io.ReadAll(over); err == nil || !over.Exceeded() {
		t.Errorf("past the cap: err=%v exceeded=%v", err, over.Exceeded())
	}
	off := NewCappedReader(strings.NewReader("abcde"), 0)
	if b, err := io.ReadAll(off); err != nil || len(b) != 5 || off.Exceeded() {
		t.Errorf("no cap: %q %v", b, err)
	}
}
