package apisvc

import (
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// Options configures the service.
type Options struct {
	// APIKey is the static X-API-Key credential required on every
	// catalog route (matching the specs' security scheme) and on the
	// /_bench/ control plane. Empty disables auth (unit tests).
	APIKey string
}

// Service is the fixture HTTP service. Create with New; it implements
// http.Handler.
type Service struct {
	catalog *apigen.Catalog
	state   *apigen.State
	routes  []route
	st      *store
	opts    Options
}

// New builds the service over the full catalog with freshly seeded state.
func New(opts Options) *Service {
	c := apigen.BuildCatalog()
	state := apigen.GenerateState(c)
	return &Service{
		catalog: c,
		state:   state,
		routes:  compileRoutes(c),
		st:      newStore(state),
		opts:    opts,
	}
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// ServeHTTP authenticates, routes to the control plane or the catalog
// surface, and access-logs every catalog request for the harness's
// failure taxonomy.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.opts.APIKey != "" && r.Header.Get("X-API-Key") != s.opts.APIKey {
		writeError(w, http.StatusUnauthorized, "missing or invalid X-API-Key")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_bench/") {
		s.handleControl(w, r)
		return
	}
	op, id, ok := matchRoute(s.routes, r)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	if !ok {
		writeError(rec, http.StatusNotFound, "no such endpoint")
		s.st.logRequest(RequestLogEntry{Method: r.Method, Path: r.URL.Path, Status: rec.status})
		return
	}
	if op.Gold || op.Resource == "" {
		s.handleGold(rec, r, op.ID, id)
	} else {
		s.handleDistractor(rec, r, op, id)
	}
	s.st.logRequest(RequestLogEntry{Method: r.Method, Path: r.URL.Path, Status: rec.status, OperationID: op.ID})
}

// handleControl serves the harness-only control plane: reset, state
// dumps, and the access log. These paths are absent from every spec.
func (s *Service) handleControl(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/_bench/reset":
		s.st.mu.Lock()
		s.st.seed(s.state)
		s.st.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"reset": true})
	case r.Method == http.MethodGet && r.URL.Path == "/_bench/requests":
		s.st.mu.Lock()
		reqs := append([]RequestLogEntry(nil), s.st.requests...)
		s.st.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_bench/state/"):
		s.handleStateDump(w, strings.TrimPrefix(r.URL.Path, "/_bench/state/"))
	default:
		writeError(w, http.StatusNotFound, "unknown control route")
	}
}

// handleStateDump dumps one state collection: "customers", "orders", or a
// distractor resource key ("family/plural").
func (s *Service) handleStateDump(w http.ResponseWriter, key string) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	switch key {
	case "customers":
		writeJSON(w, http.StatusOK, map[string]any{"rows": s.st.customers})
	case "orders":
		writeJSON(w, http.StatusOK, map[string]any{"rows": s.st.orders})
	default:
		rows, ok := s.st.distractors[key]
		if !ok {
			writeError(w, http.StatusNotFound, "unknown state collection "+key)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
	}
}
