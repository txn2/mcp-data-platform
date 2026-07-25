package apisvc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// Surface selects which catalog the service serves.
type Surface string

const (
	// SurfaceAPIStudy is the #1027 API-connection study's full tier-2
	// catalog. It is the zero value, so existing callers are unaffected.
	SurfaceAPIStudy Surface = ""
	// SurfacePerishable is the #1054 perishable-knowledge study's
	// catalog: the gold surface, a tier-0 distractor pack, and the
	// insights family whose state the world control plane mutates.
	SurfacePerishable Surface = "perishable"
)

// Options configures the service.
type Options struct {
	// APIKey is the static X-API-Key credential required on every
	// catalog route (matching the specs' security scheme) and on the
	// /_bench/ control plane. Empty disables auth (unit tests).
	APIKey string
	// Surface selects the catalog. The zero value serves the #1027
	// catalog.
	Surface Surface
	// WorldProfile names the world the perishable surface starts (and
	// resets) in. Empty means apigen.DefaultWorldProfile. An unknown name
	// is an error from New.
	WorldProfile string
}

// Service is the fixture HTTP service. Create with New; it implements
// http.Handler.
type Service struct {
	catalog *apigen.Catalog
	state   *apigen.State
	fixture *apigen.Fixture
	routes  []route
	st      *store
	opts    Options
}

// New builds the service over the selected catalog with freshly seeded
// state. It panics on an unknown world profile: the profile comes from a
// run script, and silently substituting a default would run a cell that is
// not the one the study asked for.
func New(opts Options) *Service {
	c := apigen.BuildCatalog()
	if opts.Surface == SurfacePerishable {
		c = apigen.BuildPerishableCatalog()
	}
	name := opts.WorldProfile
	if name == "" {
		name = apigen.DefaultWorldProfile
	}
	world, ok := apigen.WorldByName(name)
	if !ok {
		panic("apisvc: unknown world profile " + name)
	}
	state := apigen.GenerateState(c)
	return &Service{
		catalog: c,
		state:   state,
		fixture: apigen.BuildFixture(),
		routes:  compileRoutes(c),
		st:      newStore(state, world),
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
// dumps, the access log, and the world-change plane. These paths are
// absent from every spec.
func (s *Service) handleControl(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/_bench/reset":
		s.handleReset(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/_bench/requests":
		s.st.mu.Lock()
		reqs := append([]RequestLogEntry(nil), s.st.requests...)
		s.st.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/_bench/state/"):
		s.handleStateDump(w, strings.TrimPrefix(r.URL.Path, "/_bench/state/"))
	default:
		s.handleWorldControl(w, r)
	}
}

// handleReset restores seed state and clears the access log. An optional
// {"profile": "..."} body resets into a named world instead of the
// service's starting one, so an attempt is set up in a single call.
func (s *Service) handleReset(w http.ResponseWriter, r *http.Request) {
	body, err := decodeControlBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	world := s.st.initialWorld
	if body.Profile != "" {
		var ok bool
		if world, ok = apigen.WorldByName(body.Profile); !ok {
			writeError(w, http.StatusBadRequest, "unknown world profile "+body.Profile)
			return
		}
	}
	s.st.mu.Lock()
	s.st.initialWorld = world
	s.st.seed(s.state)
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"reset": true, "world": world})
}

// handleWorldControl serves the world-change plane (#1054): reading the
// current world, mutating it between sessions, and labeling the access log
// with the session phase.
func (s *Service) handleWorldControl(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/_bench/world":
		writeJSON(w, http.StatusOK, s.st.currentWorld())
	case r.Method == http.MethodPost && r.URL.Path == "/_bench/world":
		s.handleSetWorld(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/_bench/phase":
		s.handleSetPhase(w, r)
	default:
		writeError(w, http.StatusNotFound, "unknown control route")
	}
}

// handleSetWorld mutates the world in place. It deliberately leaves the
// access log and the rest of the state alone: this is the between-sessions
// world change that makes a stored belief stale, not a reset, and the log
// has to span both sessions for a verification to be detectable.
func (s *Service) handleSetWorld(w http.ResponseWriter, r *http.Request) {
	body, err := decodeControlBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	world, ok := apigen.WorldByName(body.Profile)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown world profile "+body.Profile)
		return
	}
	s.st.mu.Lock()
	s.st.world = world
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, world)
}

// handleSetPhase labels subsequent access-log entries.
func (s *Service) handleSetPhase(w http.ResponseWriter, r *http.Request) {
	body, err := decodeControlBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Phase == "" {
		writeError(w, http.StatusBadRequest, "phase is required")
		return
	}
	s.st.mu.Lock()
	s.st.phase = body.Phase
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"phase": body.Phase})
}

// controlBody is the union of the control plane's small request bodies.
type controlBody struct {
	Profile string `json:"profile"`
	Phase   string `json:"phase"`
}

// decodeControlBody decodes an optional JSON control body. An absent body
// decodes to the zero value: reset is called with no body by the #1027
// harness and must stay that way.
func decodeControlBody(r *http.Request) (controlBody, error) {
	var body controlBody
	err := json.NewDecoder(r.Body).Decode(&body)
	if errors.Is(err, io.EOF) {
		return controlBody{}, nil
	}
	if err != nil {
		return controlBody{}, errors.New("invalid JSON body")
	}
	return body, nil
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
