// Package appbundle moves an application between deployments (#2412): export
// writes one release as a bundle, a JSON managed resource holding the
// release's migrations, script versions with their endpoint bindings and
// schedules, and asset versions, with the connections, secrets, webhook
// sources and bot checks they use named; import reads a bundle, maps every
// name to one on this deployment, and creates or releases the application.
//
// A bundle carries no secret value, credential, data row, script state or
// run history: export reads none of them. It reads a script version's source,
// a migration's SQL and an asset's content, which are what the application is
// made of; a migration carrying a literal password is refused at export as
// it is when it is added (#2411).
package appbundle

import (
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Format identifies a bundle. Version 2 lists every handler of each script
// (#2441); version 1 carried at most one endpoint, one schedule and one
// webhook source per script, and import still reads it.
const (
	Format        = "mcp-data-platform.application"
	FormatVersion = 2
	// FormatVersion1 is the version before handlers.
	FormatVersion1 = 1
)

// MaxBytes bounds a bundle.
const MaxBytes = 64 << 20

// Bundle is one exported release.
type Bundle struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	Application   AppInfo   `json:"application"`
	Release       int       `json:"release"`
	Note          string    `json:"note,omitempty"`
	ExportedAt    time.Time `json:"exported_at"`
	Schema        *Schema   `json:"schema,omitempty"`
	Scripts       []Script  `json:"scripts"`
	Assets        []Asset   `json:"assets"`
	// Sources is the webhook sources that are parts of the application, by
	// name; each is mapped to one on the importing deployment.
	Sources []string `json:"sources"`
	Names   Names    `json:"names"`
}

// AppInfo is the application's own record.
type AppInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Schema is the application database schema and its migrations up to the
// release.
type Schema struct {
	Connection string      `json:"connection"`
	Schema     string      `json:"schema"`
	Migrations []Migration `json:"migrations"`
}

// Migration is one migration as the release holds it.
type Migration struct {
	Version       int    `json:"version"`
	Name          string `json:"name"`
	Up            string `json:"up"`
	Down          string `json:"down,omitempty"`
	Transactional bool   `json:"transactional"`
}

// Script is one script at its pinned version.
type Script struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name,omitempty"`
	Description string         `json:"description,omitempty"`
	Category    string         `json:"category,omitempty"`
	Tags        []string       `json:"tags"`
	Source      string         `json:"source"`
	Params      []script.Param `json:"params"`
	Exclusive   string         `json:"exclusive,omitempty"`
	Version     int            `json:"version"`
	// Handlers are what start the script: its endpoints, schedules and
	// webhook sources (format version 2). A tool-call handler governs a
	// deployment's people rather than an application, and is never exported.
	Handlers *Handlers `json:"handlers,omitempty"`
	// EventSource, Endpoint and Schedule are a version 1 bundle's one of
	// each, which import reads as handlers.
	EventSource string    `json:"event_source,omitempty"`
	Endpoint    *Endpoint `json:"endpoint,omitempty"`
	Schedule    *Schedule `json:"schedule,omitempty"`
	// DynamicConnections is set when the source names a connection in a way
	// a static read cannot see; those names are not in Names.
	DynamicConnections bool `json:"dynamic_connections,omitempty"`
	// Recordings are the recorded runs the source's tests replay, so the
	// importing deployment's save gate runs the same tests (#2476).
	Recordings []Recording `json:"recordings,omitempty"`
}

// Recording is one recorded run a script's tests replay: its run id, as the
// tests name it, and the recording as the platform writes it (gzipped JSON
// lines, base64 in the bundle). It holds the answers the run's calls were
// given; a secret is kept only as its {{secret:name}} placeholder.
type Recording struct {
	RunID string `json:"run_id"`
	Data  []byte `json:"data" swaggertype:"string" format:"byte"`
}

// Endpoint is a script's endpoint binding.
type Endpoint struct {
	Name            string   `json:"name"`
	Methods         []string `json:"methods"`
	Path            string   `json:"path"`
	AuthMode        string   `json:"auth_mode"`
	Callers         []string `json:"callers"`
	BotCheck        string   `json:"bot_check,omitempty"`
	CheckedMethods  []string `json:"checked_methods"`
	TimeoutSeconds  int      `json:"timeout_seconds,omitempty"`
	MaxMemoryBytes  int64    `json:"max_memory_bytes,omitempty"`
	MaxBodyBytes    int      `json:"max_body_bytes,omitempty"`
	AllowedOrigins  []string `json:"allowed_origins"`
	RateLimit       int      `json:"rate_limit,omitempty"`
	CallerRateLimit int      `json:"caller_rate_limit,omitempty"`
	Enabled         bool     `json:"enabled"`
	// ResponseMode and PageCDNs make the endpoint a browser page (#2517).
	ResponseMode string   `json:"response_mode,omitempty"`
	PageCDNs     []string `json:"page_cdns,omitempty"`
	// PageFrameOrigins, PageConnectOrigins and PageFormOrigins are a page's
	// frame, connect and form origins (#2554), and IPRateLimit one client
	// address's requests a minute (#2556).
	PageFrameOrigins   []string `json:"page_frame_origins,omitempty"`
	PageConnectOrigins []string `json:"page_connect_origins,omitempty"`
	PageFormOrigins    []string `json:"page_form_origins,omitempty"`
	IPRateLimit        int      `json:"ip_rate_limit,omitempty"`
	// FormConnection is the connection a form's submissions table is on.
	FormConnection string `json:"form_connection,omitempty"`
}

// Handlers are a script's handlers, by kind.
type Handlers struct {
	Endpoints []Endpoint `json:"endpoints"`
	Schedules []Schedule `json:"schedules"`
	Webhooks  []Webhook  `json:"webhooks"`
}

// Webhook is a webhook handler: the source the script runs for, mapped to
// one of the importing deployment's.
type Webhook struct {
	Source  string `json:"source"`
	Enabled bool   `json:"enabled"`
}

// handlersOf is a script's handlers whatever the bundle's version: version
// 1's one endpoint, schedule and source become one handler each.
func handlersOf(s *Script) Handlers {
	if s.Handlers != nil {
		return *s.Handlers
	}
	var h Handlers
	if s.Endpoint != nil {
		h.Endpoints = append(h.Endpoints, *s.Endpoint)
	}
	if s.Schedule != nil {
		h.Schedules = append(h.Schedules, *s.Schedule)
	}
	if s.EventSource != "" {
		h.Webhooks = append(h.Webhooks, Webhook{Source: s.EventSource, Enabled: true})
	}
	return h
}

// Schedule is a script's schedule.
type Schedule struct {
	CronSpec string         `json:"cron_spec"`
	Timezone string         `json:"timezone"`
	Params   map[string]any `json:"params,omitempty"`
	Enabled  bool           `json:"enabled"`
}

// Asset is one asset at its pinned version.
type Asset struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	ContentType string   `json:"content_type"`
	Tags        []string `json:"tags"`
	Version     int      `json:"version"`
	Content     []byte   `json:"content"`
}

// Names is every name of the source deployment the bundle uses, which an
// import maps to names on its own.
type Names struct {
	Connections []string `json:"connections"`
	Secrets     []string `json:"secrets"`
	Sources     []string `json:"sources"`
	BotChecks   []string `json:"bot_checks"`
}

// All is every name, sorted and once.
func (n Names) All() []string {
	all := slices.Concat(n.Connections, n.Secrets, n.Sources, n.BotChecks)
	slices.Sort(all)
	return slices.Compact(all)
}

// nameSet collects names.
type nameSet map[string]bool

func (s nameSet) add(names ...string) {
	for _, n := range names {
		if n != "" {
			s[n] = true
		}
	}
}

func (s nameSet) sorted() []string {
	out := make([]string, 0, len(s))
	for n := range s {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
