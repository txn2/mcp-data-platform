package headless

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The answer budget holds MaxFileBytes per file being read and only what was
// read while it is sent, so a page asking for many files at once waits for
// room rather than holding them all (#2721).
func TestBudget_HoldsWhatIsBeingAnsweredAndNoMore(t *testing.T) {
	r := &Renderer{}
	ctx := context.Background()
	held := make([]*budget, 0, answerBudget/MaxFileBytes)
	for range answerBudget / MaxFileBytes {
		b, err := r.take(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, b)
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := r.take(short); err == nil {
		t.Fatal("a file was read past the budget")
	}

	// One answer that read a small file gives the rest back at once: another
	// file can be read while it is still being sent.
	held[0].fit(1024)
	held[0].fit(1 << 30) // growing is never asked of a budget
	if held[0].held != 1024 {
		t.Fatalf("held = %d after fitting 1024 bytes", held[0].held)
	}
	short2, cancel2 := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel2()
	if _, err := r.take(short2); err == nil {
		t.Fatal("the bytes given back were less than a file, yet a whole file was taken")
	}
	for _, b := range held {
		b.release()
		b.release() // a second release gives nothing back twice
	}
	next, err := r.take(ctx)
	if err != nil {
		t.Fatalf("the budget was not given back: %v", err)
	}
	next.release()

	var none *budget
	none.fit(1)
	none.release()
}

func TestClampRange(t *testing.T) {
	cases := []struct{ in, want string }{
		{"bytes=0-", "bytes=0-16777215"},
		{" bytes = 1000 - ", "bytes=1000-16778215"},
		{"bytes=0-99", "bytes=0-99"},
		{"bytes=-500", "bytes=-500"},
		{"bytes=0-,10-", "bytes=0-,10-"},
		{"items=0-", "items=0-"},
	}
	for _, c := range cases {
		h := map[string]string{"range": c.in, "Accept": "*/*"}
		clampRange(h)
		if h["range"] != c.want || h["Accept"] != "*/*" {
			t.Errorf("clampRange(%q) = %q", c.in, h["range"])
		}
	}
	h := map[string]string{"Accept": "*/*"}
	clampRange(h)
	if len(h) != 1 {
		t.Errorf("a request with no range gained one: %v", h)
	}
}

// A command written into one buffer is the message a marshal of it would
// send, with and without a session (#2721).
func TestAppendFrame_IsTheMessageAMarshalWrites(t *testing.T) {
	for _, session := range []string{"", `page"1`} {
		got, err := appendFrame(nil, 7, "Fetch.fulfillRequest", session, func(dst []byte) []byte {
			return append(dst, `{"requestId":"r","body":"QUJD"}`...)
		})
		if err != nil {
			t.Fatal(err)
		}
		var m message
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("the frame %s is not JSON: %v", got, err)
		}
		if m.ID != 7 || m.Method != "Fetch.fulfillRequest" || m.SessionID != session || string(m.Params) != `{"requestId":"r","body":"QUJD"}` {
			t.Errorf("frame %s read back as %+v", got, m)
		}
		if session == "" && strings.Contains(string(got), "sessionId") {
			t.Errorf("a browser command carries a session: %s", got)
		}
	}
}
