//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/test/e2e/helpers"
)

// newStandaloneAdmin creates a platform + httptest server + admin client for standalone mode.
func newStandaloneAdmin(t *testing.T, cfg *platform.Config) (*platform.Platform, *httptest.Server, *helpers.AdminClient) {
	t.Helper()

	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("creating platform: %v", err)
	}

	handler := helpers.BuildAdminHandler(p)
	ts := httptest.NewServer(handler)

	client := helpers.NewAdminClient(ts.URL, helpers.AdminAPIKey)
	return p, ts, client
}

// TestAdminAPI_AuthEnforcement validates that auth is enforced on every endpoint group.
func TestAdminAPI_AuthEnforcement(t *testing.T) {
	cfg := helpers.StandaloneAdminConfig()
	p, ts, _ := newStandaloneAdmin(t, cfg)
	defer func() {
		ts.Close()
		_ = p.Close()
	}()

	// Representative paths across all endpoint groups
	paths := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/admin/system/info"},
		{"GET", "/api/v1/admin/tools"},
		{"GET", "/api/v1/admin/connections"},
		{"GET", "/api/v1/admin/config"},
		{"GET", "/api/v1/admin/config/mode"},
		{"GET", "/api/v1/admin/config/export"},
		{"GET", "/api/v1/admin/personas"},
		{"GET", "/api/v1/admin/personas/admin"},
		{"GET", "/api/v1/admin/auth/keys"},
	}

	t.Run("no_key_returns_401", func(t *testing.T) {
		noAuth := helpers.NewAdminClient(ts.URL, "")
		for _, p := range paths {
			status, _, err := noAuth.RawGet(p.path)
			if err != nil {
				t.Errorf("%s %s: unexpected error: %v", p.method, p.path, err)
				continue
			}
			if status != http.StatusUnauthorized {
				t.Errorf("%s %s: expected 401, got %d", p.method, p.path, status)
			}
		}
	})

	t.Run("invalid_key_returns_401", func(t *testing.T) {
		badAuth := helpers.NewAdminClient(ts.URL, "totally-invalid-key")
		for _, p := range paths {
			status, _, err := badAuth.RawGet(p.path)
			if err != nil {
				t.Errorf("%s %s: unexpected error: %v", p.method, p.path, err)
				continue
			}
			if status != http.StatusUnauthorized {
				t.Errorf("%s %s: expected 401, got %d", p.method, p.path, status)
			}
		}
	})

	t.Run("analyst_key_returns_401", func(t *testing.T) {
		// Analyst persona is not the admin persona, so PlatformAuthenticator rejects it
		analystAuth := helpers.NewAdminClient(ts.URL, helpers.AnalystAPIKey)
		for _, p := range paths {
			status, _, err := analystAuth.RawGet(p.path)
			if err != nil {
				t.Errorf("%s %s: unexpected error: %v", p.method, p.path, err)
				continue
			}
			if status != http.StatusUnauthorized {
				t.Errorf("%s %s: expected 401, got %d", p.method, p.path, status)
			}
		}
	})

	t.Run("admin_key_returns_200", func(t *testing.T) {
		adminAuth := helpers.NewAdminClient(ts.URL, helpers.AdminAPIKey)
		for _, p := range paths {
			status, _, err := adminAuth.RawGet(p.path)
			if err != nil {
				t.Errorf("%s %s: unexpected error: %v", p.method, p.path, err)
				continue
			}
			if status != http.StatusOK {
				t.Errorf("%s %s: expected 200, got %d", p.method, p.path, status)
			}
		}
	})
}

// TestAdminAPI_Standalone validates every endpoint's behavior in standalone mode (no DB).
func TestAdminAPI_Standalone(t *testing.T) {
	cfg := helpers.StandaloneAdminConfig()
	p, ts, client := newStandaloneAdmin(t, cfg)
	defer func() {
		ts.Close()
		_ = p.Close()
	}()

	// --- System ---

	t.Run("system_info", func(t *testing.T) {
		info, status, err := client.SystemInfo()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if info.ConfigMode != "file" {
			t.Errorf("expected config_mode=file, got %s", info.ConfigMode)
		}
		if info.Features.Database {
			t.Error("expected database=false")
		}
		if info.Features.Audit {
			t.Error("expected audit=false in standalone")
		}
		if info.Features.Knowledge {
			t.Error("expected knowledge=false in standalone")
		}
		if info.PersonaCount != 2 {
			t.Errorf("expected persona_count=2, got %d", info.PersonaCount)
		}
	})

	t.Run("list_tools_empty", func(t *testing.T) {
		tools, status, err := client.ListTools()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if tools.Total != 0 {
			t.Errorf("expected 0 tools, got %d", tools.Total)
		}
	})

	t.Run("list_connections_empty", func(t *testing.T) {
		conns, status, err := client.ListConnections()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if conns.Total != 0 {
			t.Errorf("expected 0 connections, got %d", conns.Total)
		}
	})

	// --- Config ---

	t.Run("get_config", func(t *testing.T) {
		cfg, status, err := client.GetConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if cfg == nil {
			t.Fatal("expected non-nil config")
		}
	})

	t.Run("get_config_mode", func(t *testing.T) {
		mode, status, err := client.GetConfigMode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if mode.Mode != "file" {
			t.Errorf("expected mode=file, got %s", mode.Mode)
		}
		if !mode.ReadOnly {
			t.Error("expected read_only=true")
		}
	})

	t.Run("export_config", func(t *testing.T) {
		body, status, err := client.ExportConfig(false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if body == "" {
			t.Error("expected non-empty YAML body")
		}
	})

	t.Run("import_config_409", func(t *testing.T) {
		// ConfigStore is a FileStore, so import route is registered but blocked
		_, status, err := client.ImportConfig("apiVersion: v2", "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("config_history_409", func(t *testing.T) {
		_, status, err := client.ConfigHistory()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	// --- Personas ---

	t.Run("list_personas", func(t *testing.T) {
		list, status, err := client.ListPersonas()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if list.Total != 2 {
			t.Errorf("expected 2 personas, got %d", list.Total)
		}
	})

	t.Run("get_persona_admin", func(t *testing.T) {
		detail, status, err := client.GetPersona("admin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if detail.Name != "admin" {
			t.Errorf("expected name=admin, got %s", detail.Name)
		}
	})

	t.Run("get_persona_not_found", func(t *testing.T) {
		_, status, err := client.GetPersona("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("expected 404, got %d", status)
		}
	})

	t.Run("create_persona_405", func(t *testing.T) {
		// Write routes not registered in file mode
		status, err := client.RawPost("/api/v1/admin/personas", helpers.PersonaCreateRequest{
			Name: "new", DisplayName: "New",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	t.Run("update_persona_405", func(t *testing.T) {
		status, err := client.RawPut("/api/v1/admin/personas/analyst", helpers.PersonaCreateRequest{
			DisplayName: "Updated",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	t.Run("delete_persona_405", func(t *testing.T) {
		status, err := client.RawDelete("/api/v1/admin/personas/analyst")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	// --- Auth keys ---

	t.Run("list_auth_keys", func(t *testing.T) {
		keys, status, err := client.ListAuthKeys()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if keys.Total != 2 {
			t.Errorf("expected 2 keys, got %d", keys.Total)
		}
	})

	t.Run("create_auth_key_405", func(t *testing.T) {
		status, err := client.RawPost("/api/v1/admin/auth/keys", helpers.AuthKeyCreateRequest{
			Name: "new", Roles: []string{"viewer"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	t.Run("delete_auth_key_405", func(t *testing.T) {
		status, err := client.RawDelete("/api/v1/admin/auth/keys/analyst")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	// --- Audit ---

	t.Run("audit_events_409", func(t *testing.T) {
		_, status, err := client.ListAuditEvents("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("audit_event_by_id_409", func(t *testing.T) {
		status, err := client.GetAuditEvent("some-id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("audit_stats_409", func(t *testing.T) {
		_, status, err := client.GetAuditStats()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	// --- Knowledge ---

	t.Run("knowledge_insights_409", func(t *testing.T) {
		status, _, err := client.RawGet("/api/v1/admin/knowledge/insights")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})
}

// TestAdminAPI_FileDB validates endpoints in file+DB mode (file config, database available).
func TestAdminAPI_FileDB(t *testing.T) {
	pgDSN := helpers.StartPostgres(t)

	adb := helpers.NewAdminTestDB(t, pgDSN)
	defer func() {
		if err := adb.Close(); err != nil {
			t.Errorf("closing admin test db: %v", err)
		}
	}()
	adb.TruncateAdminTables(t)

	cfg := helpers.FileDBAdminConfig(pgDSN)
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("creating platform: %v", err)
	}
	defer func() { _ = p.Close() }()

	handler := helpers.BuildAdminHandler(p)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := helpers.NewAdminClient(ts.URL, helpers.AdminAPIKey)

	// --- System info: DB features enabled ---

	t.Run("system_info_db_features", func(t *testing.T) {
		info, status, err := client.SystemInfo()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if !info.Features.Database {
			t.Error("expected database=true")
		}
		if !info.Features.Audit {
			t.Error("expected audit=true")
		}
		if info.ConfigMode != "file" {
			t.Errorf("expected config_mode=file, got %s", info.ConfigMode)
		}
	})

	// --- Config mode is file (read-only) ---

	t.Run("config_mode_file", func(t *testing.T) {
		mode, status, err := client.GetConfigMode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if mode.Mode != "file" {
			t.Errorf("expected mode=file, got %s", mode.Mode)
		}
		if !mode.ReadOnly {
			t.Error("expected read_only=true")
		}
	})

	t.Run("import_config_blocked_file_mode", func(t *testing.T) {
		_, status, err := client.ImportConfig("apiVersion: v2", "test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("config_history_blocked_file_mode", func(t *testing.T) {
		_, status, err := client.ConfigHistory()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	// --- Persona writes blocked in file mode ---

	t.Run("create_persona_blocked_file_mode", func(t *testing.T) {
		status, err := client.RawPost("/api/v1/admin/personas", helpers.PersonaCreateRequest{
			Name: "new", DisplayName: "New",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", status)
		}
	})

	// --- Audit: real DB ---

	t.Run("audit_events_empty", func(t *testing.T) {
		events, status, err := client.ListAuditEvents("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if events.Total != 0 {
			t.Errorf("expected 0 events, got %d", events.Total)
		}
	})

	// Seed 2 audit events
	now := time.Now().UTC()
	event1 := audit.Event{
		ID:        "e2e-audit-001",
		Timestamp: now,
		ToolName:  "trino_query",
		UserID:    "e2e-user",
		Success:   true,
	}
	event2 := audit.Event{
		ID:        "e2e-audit-002",
		Timestamp: now.Add(time.Second),
		ToolName:  "datahub_search",
		UserID:    "e2e-user",
		Success:   false,
	}
	adb.InsertAuditEvent(t, event1)
	adb.InsertAuditEvent(t, event2)

	t.Run("audit_events_list", func(t *testing.T) {
		events, status, err := client.ListAuditEvents("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if events.Total != 2 {
			t.Errorf("expected 2 events, got %d", events.Total)
		}
	})

	t.Run("audit_event_by_id", func(t *testing.T) {
		status, err := client.GetAuditEvent("e2e-audit-001")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Errorf("expected 200, got %d", status)
		}
	})

	t.Run("audit_event_not_found", func(t *testing.T) {
		status, err := client.GetAuditEvent("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("expected 404, got %d", status)
		}
	})

	t.Run("audit_filter_by_tool_name", func(t *testing.T) {
		events, status, err := client.ListAuditEvents("tool_name=trino_query")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if events.Total != 1 {
			t.Errorf("expected 1 event, got %d", events.Total)
		}
	})

	t.Run("audit_pagination", func(t *testing.T) {
		events, status, err := client.ListAuditEvents("per_page=1&page=1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if len(events.Data) != 1 {
			t.Errorf("expected 1 event in page, got %d", len(events.Data))
		}
		if events.Total != 2 {
			t.Errorf("expected total=2, got %d", events.Total)
		}
	})

	t.Run("audit_stats", func(t *testing.T) {
		stats, status, err := client.GetAuditStats()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if stats.Total != 2 {
			t.Errorf("expected total=2, got %d", stats.Total)
		}
		if stats.Success != 1 {
			t.Errorf("expected success=1, got %d", stats.Success)
		}
		if stats.Failures != 1 {
			t.Errorf("expected failures=1, got %d", stats.Failures)
		}
	})
}

// TestAdminAPI_BootstrapDB validates full CRUD in bootstrap+DB mode.
func TestAdminAPI_BootstrapDB(t *testing.T) {
	pgDSN := helpers.StartPostgres(t)

	adb := helpers.NewAdminTestDB(t, pgDSN)
	defer func() {
		if err := adb.Close(); err != nil {
			t.Errorf("closing admin test db: %v", err)
		}
	}()
	adb.TruncateAdminTables(t)

	cfg := helpers.BootstrapDBAdminConfig(pgDSN)
	p, err := platform.New(platform.WithConfig(cfg))
	if err != nil {
		t.Fatalf("creating platform: %v", err)
	}
	defer func() { _ = p.Close() }()

	handler := helpers.BuildAdminHandler(p)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := helpers.NewAdminClient(ts.URL, helpers.AdminAPIKey)

	// --- System info ---

	t.Run("system_info_database_mode", func(t *testing.T) {
		info, status, err := client.SystemInfo()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if info.ConfigMode != "database" {
			t.Errorf("expected config_mode=database, got %s", info.ConfigMode)
		}
		if !info.Features.Database {
			t.Error("expected database=true")
		}
	})

	t.Run("config_mode_database", func(t *testing.T) {
		mode, status, err := client.GetConfigMode()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if mode.Mode != "database" {
			t.Errorf("expected mode=database, got %s", mode.Mode)
		}
		if mode.ReadOnly {
			t.Error("expected read_only=false")
		}
	})

	// --- Config import/history ---

	t.Run("config_import_valid", func(t *testing.T) {
		validYAML := `apiVersion: v2
server:
  name: e2e-import-test
  transport: stdio
`
		result, status, err := client.ImportConfig(validYAML, "e2e-import")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Errorf("expected 200, got %d", status)
		}
		if result != nil && result.Status != "saved" {
			t.Errorf("expected status=saved, got %s", result.Status)
		}
	})

	t.Run("config_import_invalid", func(t *testing.T) {
		_, status, err := client.ImportConfig("{{invalid yaml", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", status)
		}
	})

	t.Run("config_history", func(t *testing.T) {
		history, status, err := client.ConfigHistory()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		// At least the seed and our import
		if history.Total < 1 {
			t.Errorf("expected at least 1 revision, got %d", history.Total)
		}
	})

	// --- Persona CRUD ---

	t.Run("persona_create", func(t *testing.T) {
		detail, status, err := client.CreatePersona(helpers.PersonaCreateRequest{
			Name:        "e2e-engineer",
			DisplayName: "E2E Engineer",
			Roles:       []string{"engineer"},
			AllowTools:  []string{"trino_*"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusCreated {
			t.Fatalf("expected 201, got %d", status)
		}
		if detail.Name != "e2e-engineer" {
			t.Errorf("expected name=e2e-engineer, got %s", detail.Name)
		}
	})

	t.Run("persona_create_duplicate", func(t *testing.T) {
		_, status, err := client.CreatePersona(helpers.PersonaCreateRequest{
			Name:        "e2e-engineer",
			DisplayName: "E2E Engineer Duplicate",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("persona_create_missing_name", func(t *testing.T) {
		_, status, err := client.CreatePersona(helpers.PersonaCreateRequest{
			DisplayName: "No Name",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", status)
		}
	})

	t.Run("persona_list_includes_new", func(t *testing.T) {
		list, status, err := client.ListPersonas()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if list.Total != 3 {
			t.Errorf("expected 3 personas, got %d", list.Total)
		}
	})

	t.Run("persona_get_new", func(t *testing.T) {
		detail, status, err := client.GetPersona("e2e-engineer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if detail.DisplayName != "E2E Engineer" {
			t.Errorf("expected display_name=E2E Engineer, got %s", detail.DisplayName)
		}
	})

	t.Run("persona_update", func(t *testing.T) {
		detail, status, err := client.UpdatePersona("e2e-engineer", helpers.PersonaCreateRequest{
			DisplayName: "E2E Senior Engineer",
			AllowTools:  []string{"trino_*", "datahub_*"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if detail.DisplayName != "E2E Senior Engineer" {
			t.Errorf("expected updated display_name, got %s", detail.DisplayName)
		}
	})

	t.Run("persona_delete", func(t *testing.T) {
		status, err := client.DeletePersona("e2e-engineer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Errorf("expected 200, got %d", status)
		}
	})

	t.Run("persona_deleted_not_found", func(t *testing.T) {
		_, status, err := client.GetPersona("e2e-engineer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("expected 404, got %d", status)
		}
	})

	t.Run("persona_delete_admin_blocked", func(t *testing.T) {
		status, err := client.DeletePersona("admin")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("persona_delete_nonexistent", func(t *testing.T) {
		status, err := client.DeletePersona("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("expected 404, got %d", status)
		}
	})

	// --- Auth key CRUD ---

	t.Run("auth_key_list_initial", func(t *testing.T) {
		keys, status, err := client.ListAuthKeys()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if keys.Total != 2 {
			t.Errorf("expected 2 keys, got %d", keys.Total)
		}
	})

	t.Run("auth_key_create", func(t *testing.T) {
		result, status, err := client.CreateAuthKey("e2e-viewer", []string{"viewer"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusCreated {
			t.Fatalf("expected 201, got %d", status)
		}
		if result.Key == "" {
			t.Error("expected non-empty key value")
		}
		if result.Warning == "" {
			t.Error("expected non-empty warning")
		}
		if result.Name != "e2e-viewer" {
			t.Errorf("expected name=e2e-viewer, got %s", result.Name)
		}
	})

	t.Run("auth_key_create_duplicate", func(t *testing.T) {
		_, status, err := client.CreateAuthKey("e2e-viewer", []string{"viewer"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusConflict {
			t.Errorf("expected 409, got %d", status)
		}
	})

	t.Run("auth_key_create_empty_name", func(t *testing.T) {
		_, status, err := client.CreateAuthKey("", []string{"viewer"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", status)
		}
	})

	t.Run("auth_key_create_empty_roles", func(t *testing.T) {
		_, status, err := client.CreateAuthKey("no-roles", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", status)
		}
	})

	t.Run("auth_key_list_after_create", func(t *testing.T) {
		keys, status, err := client.ListAuthKeys()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if keys.Total != 3 {
			t.Errorf("expected 3 keys, got %d", keys.Total)
		}
	})

	t.Run("auth_key_delete", func(t *testing.T) {
		status, err := client.DeleteAuthKey("e2e-viewer")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Errorf("expected 200, got %d", status)
		}
	})

	t.Run("auth_key_delete_nonexistent", func(t *testing.T) {
		status, err := client.DeleteAuthKey("nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusNotFound {
			t.Errorf("expected 404, got %d", status)
		}
	})

	t.Run("auth_key_list_after_delete", func(t *testing.T) {
		keys, status, err := client.ListAuthKeys()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if keys.Total != 2 {
			t.Errorf("expected 2 keys, got %d", keys.Total)
		}
	})
}

// TestAdminAPI_ConfigRedaction validates that sensitive config values are redacted.
func TestAdminAPI_ConfigRedaction(t *testing.T) {
	cfg := helpers.StandaloneAdminConfig()
	p, ts, client := newStandaloneAdmin(t, cfg)
	defer func() {
		ts.Close()
		_ = p.Close()
	}()

	t.Run("get_config_redacts_keys", func(t *testing.T) {
		configMap, status, err := client.GetConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}

		// Walk the config looking for key values that should be redacted
		configJSON, _ := json.Marshal(configMap)
		configStr := string(configJSON)
		if strings.Contains(configStr, helpers.AdminAPIKey) {
			t.Error("config response contains un-redacted admin API key")
		}
		if strings.Contains(configStr, helpers.AnalystAPIKey) {
			t.Error("config response contains un-redacted analyst API key")
		}
	})

	t.Run("export_config_redacted_by_default", func(t *testing.T) {
		body, status, err := client.ExportConfig(false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if strings.Contains(body, helpers.AdminAPIKey) {
			t.Error("exported YAML contains un-redacted admin API key")
		}
		if strings.Contains(body, helpers.AnalystAPIKey) {
			t.Error("exported YAML contains un-redacted analyst API key")
		}
		if !strings.Contains(body, "[REDACTED]") {
			t.Error("exported YAML does not contain [REDACTED] placeholder")
		}
	})

	t.Run("export_config_with_secrets", func(t *testing.T) {
		body, status, err := client.ExportConfig(true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != 200 {
			t.Fatalf("expected 200, got %d", status)
		}
		if !strings.Contains(body, helpers.AdminAPIKey) {
			t.Error("exported YAML with secrets=true should contain admin API key")
		}
		if !strings.Contains(body, helpers.AnalystAPIKey) {
			t.Error("exported YAML with secrets=true should contain analyst API key")
		}
	})
}
