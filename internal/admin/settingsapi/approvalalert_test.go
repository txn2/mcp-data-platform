package settingsapi

import (
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/reviewalert"
)

// TestApprovalAlert_ReadAndWrite: the approvals queue alert has its own
// settings route, its own defaults and its own stored section (#2437).
func TestApprovalAlert_ReadAndWrite(t *testing.T) {
	t.Parallel()
	store := &fakeReviewAlert{}
	mux := testMux(Config{ApprovalAlert: store, Mutable: true})
	res := doJSON(t, mux, http.MethodGet, approvalAlertPath, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", res.Code)
	}
	if v := decodeView(t, res.Body.Bytes()); v.OldestPendingDays != reviewalert.DefaultApprovalOldestDays {
		t.Errorf("the unconfigured view must carry the approvals default age: %+v", v)
	}
	res = doJSON(t, mux, http.MethodPut, approvalAlertPath, map[string]any{
		"enabled": true, "pending_threshold": 5, "oldest_pending_days": 1, "cooldown_hours": 12, "recipients": []string{"Ops@Example.com"},
	})
	if res.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", res.Code, res.Body.String())
	}
	if store.lastSet == nil || store.lastSet.OldestPendingDays != 1 {
		t.Errorf("the PUT did not store the approvals settings: %+v", store.lastSet)
	}
	if res := doJSON(t, testMux(Config{Mutable: true}), http.MethodGet, approvalAlertPath, nil); res.Code != http.StatusNotFound {
		t.Errorf("with no store the route must not exist: %d", res.Code)
	}
}
