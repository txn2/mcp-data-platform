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
