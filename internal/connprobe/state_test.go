package connprobe

import (
	"errors"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/connstate"
)

func TestStateOfGET(t *testing.T) {
	for _, tc := range []struct {
		status   int
		declared bool
		want     string
	}{
		{401, false, connstate.AuthFailed},
		{407, true, connstate.AuthFailed},
		{403, true, connstate.AuthFailed},
		{403, false, ""},
		{200, true, connstate.Healthy},
		{200, false, ""},
		{404, true, connstate.Healthy},
		{503, false, connstate.Unreachable},
		{502, true, connstate.Unreachable},
	} {
		if got := stateOfGET(tc.status, tc.declared); got != tc.want {
			t.Errorf("stateOfGET(%d, %v) = %q, want %q", tc.status, tc.declared, got, tc.want)
		}
	}
}

func TestRecordGETAndUnreached(t *testing.T) {
	RecordGET("api", "probe-2443-declared", http.StatusForbidden, true)
	if s, _ := connstate.State("api", "probe-2443-declared"); s != connstate.AuthFailed {
		t.Errorf("a 403 on a declared read recorded %q", s)
	}
	RecordGET("api", "probe-2443-root", http.StatusOK, false)
	if s, seen := connstate.State("api", "probe-2443-root"); seen {
		t.Errorf("a 200 at the base recorded %q", s)
	}
	Unreached("api", "probe-2443-gone", errors.New("connection refused"))
	if s, _ := connstate.State("api", "probe-2443-gone"); s != connstate.Unreachable {
		t.Errorf("an unreached probe recorded %q", s)
	}
}
