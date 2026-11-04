package scripttx

import (
	"testing"
	"time"
)

func TestJitterStaysBelowItsLimit(t *testing.T) {
	if jitter(0) != 0 || jitter(-time.Second) != 0 {
		t.Fatal("no limit, no jitter")
	}
	for range 100 {
		if d := jitter(25 * time.Millisecond); d < 0 || d >= 25*time.Millisecond {
			t.Fatalf("jitter %s outside [0, 25ms)", d)
		}
	}
}

func TestCountReadsEachNumberForm(t *testing.T) {
	for _, v := range []any{int64(4), 4, float64(4)} {
		if got := count(v); got != 4 {
			t.Errorf("count(%T 4) = %d", v, got)
		}
	}
	if count("nope") != 0 {
		t.Error("a value that is no number counts as 0")
	}
}

func TestHostHooksAreNilSafe(t *testing.T) {
	h := &Host{}
	if err := h.check(nil, "x"); err != nil {
		t.Fatalf("a host with no meter refuses: %v", err)
	}
	h.hold(nil)
	(&rows{}).Freeze()
}
