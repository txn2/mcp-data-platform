package timeoutcap

import (
	"testing"
	"time"
)

func TestApply(t *testing.T) {
	cases := []struct {
		name      string
		ceiling   time.Duration
		requested int
		want      time.Duration
		wantCap   Cap
	}{
		{"nothing asked keeps the ceiling", 2 * time.Minute, 0, 2 * time.Minute, Cap{}},
		{"less is honored", 2 * time.Minute, 30, 30 * time.Second, Cap{}},
		{"exactly the ceiling is not a cap", 2 * time.Minute, 120, 2 * time.Minute, Cap{}},
		{"more is capped and reported", 2 * time.Minute, 240, 2 * time.Minute, Cap{AppliedSeconds: 120, CappedBy: CappedByCallTimeout, RequestedSeconds: 240}},
		{"a sub-second ceiling is reported as a whole second", 500 * time.Millisecond, 5, 500 * time.Millisecond, Cap{AppliedSeconds: 1, CappedBy: CappedByCallTimeout, RequestedSeconds: 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, capped := Apply(tc.ceiling, tc.requested, CappedByCallTimeout)
			if got != tc.want || capped != tc.wantCap {
				t.Errorf("Apply = %v, %+v; want %v, %+v", got, capped, tc.want, tc.wantCap)
			}
			if capped.Capped() != (tc.wantCap.CappedBy != "") {
				t.Errorf("Capped() = %v", capped.Capped())
			}
		})
	}
}

func TestTimedOut(t *testing.T) {
	_, capped := Apply(2*time.Minute, 240, CappedByCallTimeout)
	if got, want := capped.TimedOut(2*time.Minute, CallTimeoutOwner), "timed out after 120s (the connection's call_timeout; timeout_seconds 240 was capped)"; got != want {
		t.Errorf("capped = %q; want %q", got, want)
	}
	if got, want := (Cap{}).TimedOut(30*time.Second, "the call's timeout_seconds"), "timed out after 30s (the call's timeout_seconds)"; got != want {
		t.Errorf("uncapped = %q; want %q", got, want)
	}
}

func TestIsTimeout(t *testing.T) {
	for msg, want := range map[string]bool{
		`Post "https://x": context deadline exceeded`:                           true,
		`Get "https://x": net/http: request canceled (Client.Timeout exceeded)`: true,
		"read tcp: i/o timeout":                                                 true,
		"connection reset by peer":                                              false,
		"the upstream said: timeout":                                            false,
	} {
		if got := IsTimeout(msg); got != want {
			t.Errorf("IsTimeout(%q) = %v; want %v", msg, got, want)
		}
	}
}

func TestOwnerAndExplain(t *testing.T) {
	_, capped := Apply(time.Second, 9, CappedByCallTimeout)
	for _, tc := range []struct {
		requested int
		c         Cap
		msg, want string
	}{
		{5, Cap{}, "context deadline exceeded", "the call's timeout_seconds"},
		{5, Cap{}, "(Client.Timeout exceeded while awaiting headers)", CallTimeoutOwner},
		{0, Cap{}, "context deadline exceeded", CallTimeoutOwner},
		{9, capped, "context deadline exceeded", CallTimeoutOwner},
	} {
		if got := Owner(tc.requested, tc.c, tc.msg); got != tc.want {
			t.Errorf("Owner(%d, %+v, %q) = %q; want %q", tc.requested, tc.c, tc.msg, got, tc.want)
		}
	}
	if got := Explain("connection refused", time.Second, 9, capped); got != "connection refused" {
		t.Errorf("a failure that is not a timeout is unchanged: %q", got)
	}
	want := "timed out after 1s (the connection's call_timeout; timeout_seconds 9 was capped): context deadline exceeded"
	if got := Explain("context deadline exceeded", time.Second, 9, capped); got != want {
		t.Errorf("Explain = %q; want %q", got, want)
	}
}
