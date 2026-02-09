package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const (
	stateNameStarting = "starting"
	stateNameReady    = "ready"
	stateNameDraining = "draining"
	goroutineCount    = 100
)

func TestNewChecker_StartsInStartingState(t *testing.T) {
	hc := NewChecker()
	if hc.State() != stateNameStarting {
		t.Errorf("State() = %q, want %q", hc.State(), stateNameStarting)
	}
	if hc.IsReady() {
		t.Error("IsReady() = true, want false in starting state")
	}
}

func TestSetReady(t *testing.T) {
	hc := NewChecker()
	hc.SetReady()
	if hc.State() != stateNameReady {
		t.Errorf("State() = %q, want %q", hc.State(), stateNameReady)
	}
	if !hc.IsReady() {
		t.Error("IsReady() = false, want true after SetReady()")
	}
}

func TestSetDraining(t *testing.T) {
	hc := NewChecker()
	hc.SetReady()
	hc.SetDraining()
	if hc.State() != stateNameDraining {
		t.Errorf("State() = %q, want %q", hc.State(), stateNameDraining)
	}
	if hc.IsReady() {
		t.Error("IsReady() = true, want false in draining state")
	}
}

func TestStateTransitions(t *testing.T) {
	hc := NewChecker()

	// starting → ready
	if hc.State() != stateNameStarting {
		t.Fatalf("initial state = %q, want %s", hc.State(), stateNameStarting)
	}
	hc.SetReady()
	if hc.State() != stateNameReady {
		t.Fatalf("after SetReady() = %q, want %s", hc.State(), stateNameReady)
	}

	// ready → draining
	hc.SetDraining()
	if hc.State() != stateNameDraining {
		t.Fatalf("after SetDraining() = %q, want %s", hc.State(), stateNameDraining)
	}

	// draining → ready (re-ready, e.g. test scenario)
	hc.SetReady()
	if hc.State() != stateNameReady {
		t.Fatalf("after re-SetReady() = %q, want %s", hc.State(), stateNameReady)
	}
}

func TestLivenessHandler_AlwaysReturns200(t *testing.T) {
	hc := NewChecker()

	tests := []struct {
		name  string
		setup func()
	}{
		{stateNameStarting, func() {}},
		{stateNameReady, func() { hc.SetReady() }},
		{stateNameDraining, func() { hc.SetDraining() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset to starting for each test
			hc.state.Store(stateStarting)
			tt.setup()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
			hc.LivenessHandler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			var resp healthResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Status != "ok" {
				t.Errorf("status = %q, want %q", resp.Status, "ok")
			}

			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestReadinessHandler_StatusCodes(t *testing.T) {
	hc := NewChecker()

	tests := []struct {
		name       string
		setup      func()
		wantCode   int
		wantStatus string
	}{
		{stateNameStarting, func() { hc.state.Store(stateStarting) }, http.StatusServiceUnavailable, stateNameStarting},
		{stateNameReady, func() { hc.SetReady() }, http.StatusOK, stateNameReady},
		{stateNameDraining, func() { hc.SetDraining() }, http.StatusServiceUnavailable, stateNameDraining},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)
			hc.ReadinessHandler().ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}

			var resp healthResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Status != tt.wantStatus {
				t.Errorf("body status = %q, want %q", resp.Status, tt.wantStatus)
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	hc := NewChecker()

	var wg sync.WaitGroup
	wg.Add(goroutineCount * 3)

	for range goroutineCount {
		go func() {
			defer wg.Done()
			hc.SetReady()
		}()
		go func() {
			defer wg.Done()
			hc.SetDraining()
		}()
		go func() {
			defer wg.Done()
			_ = hc.IsReady()
			_ = hc.State()
		}()
	}

	wg.Wait()

	// Final state should be one of the valid states
	s := hc.State()
	if s != stateNameStarting && s != stateNameReady && s != stateNameDraining {
		t.Errorf("State() = %q, not a valid state", s)
	}
}
