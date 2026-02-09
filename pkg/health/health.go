// Package health provides readiness state tracking and HTTP health check handlers.
package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// State constants for the readiness state machine.
const (
	stateStarting int32 = iota
	stateReady
	stateDraining
)

// Checker tracks the readiness state of the platform.
// It is safe for concurrent use.
type Checker struct {
	state atomic.Int32
}

// NewChecker creates a Checker in the Starting state.
func NewChecker() *Checker {
	return &Checker{}
}

// SetReady transitions to the Ready state.
func (c *Checker) SetReady() {
	c.state.Store(stateReady)
}

// SetDraining transitions to the Draining state.
func (c *Checker) SetDraining() {
	c.state.Store(stateDraining)
}

// IsReady returns true when the state is Ready.
func (c *Checker) IsReady() bool {
	return c.state.Load() == stateReady
}

// State returns the current state as a human-readable string.
func (c *Checker) State() string {
	switch c.state.Load() {
	case stateReady:
		return "ready"
	case stateDraining:
		return "draining"
	default:
		return "starting"
	}
}

// healthResponse is the JSON body returned by health endpoints.
type healthResponse struct {
	Status string `json:"status"`
}

// LivenessHandler returns an http.HandlerFunc that always responds 200 OK.
// Use this for K8s livenessProbe (/healthz).
func (*Checker) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
	}
}

// ReadinessHandler returns an http.HandlerFunc that responds 200 when ready
// and 503 when starting or draining.
// Use this for K8s readinessProbe (/readyz).
func (c *Checker) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if c.IsReady() {
			writeJSON(w, http.StatusOK, healthResponse{Status: c.State()})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, healthResponse{Status: c.State()})
	}
}

func writeJSON(w http.ResponseWriter, code int, v healthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
