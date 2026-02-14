//go:build integration

package helpers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/txn2/mcp-data-platform/pkg/admin"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/configstore"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// Test API key values used across admin e2e tests.
const (
	AdminAPIKey   = "e2e-admin-key-secret-value"
	AnalystAPIKey = "e2e-analyst-key-secret-value"
)

// --- Response types (mirrors unexported admin package types) ---

// AdminSystemInfo mirrors the admin systemInfoResponse.
type AdminSystemInfo struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Description  string              `json:"description"`
	Transport    string              `json:"transport"`
	ConfigMode   string              `json:"config_mode"`
	Features     AdminSystemFeatures `json:"features"`
	ToolkitCount int                 `json:"toolkit_count"`
	PersonaCount int                 `json:"persona_count"`
}

// AdminSystemFeatures mirrors the admin systemFeatures.
type AdminSystemFeatures struct {
	Audit     bool `json:"audit"`
	OAuth     bool `json:"oauth"`
	Knowledge bool `json:"knowledge"`
	Admin     bool `json:"admin"`
	Database  bool `json:"database"`
}

// AdminConfigMode mirrors the admin configModeResponse.
type AdminConfigMode struct {
	Mode     string `json:"mode"`
	ReadOnly bool   `json:"read_only"`
}

// AdminPersonaSummary mirrors the admin personaSummary.
type AdminPersonaSummary struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	ToolCount   int      `json:"tool_count"`
}

// AdminPersonaDetail mirrors the admin personaDetail.
type AdminPersonaDetail struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	Priority    int      `json:"priority"`
	AllowTools  []string `json:"allow_tools"`
	DenyTools   []string `json:"deny_tools"`
	Tools       []string `json:"tools"`
}

// AdminPersonaList mirrors the admin personaListResponse.
type AdminPersonaList struct {
	Personas []AdminPersonaSummary `json:"personas"`
	Total    int                   `json:"total"`
}

// AdminToolInfo mirrors the admin toolInfo.
type AdminToolInfo struct {
	Name       string `json:"name"`
	Toolkit    string `json:"toolkit"`
	Kind       string `json:"kind"`
	Connection string `json:"connection"`
}

// AdminToolList mirrors the admin toolListResponse.
type AdminToolList struct {
	Tools []AdminToolInfo `json:"tools"`
	Total int             `json:"total"`
}

// AdminConnectionInfo mirrors the admin connectionInfo.
type AdminConnectionInfo struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Connection string   `json:"connection"`
	Tools      []string `json:"tools"`
}

// AdminConnectionList mirrors the admin connectionListResponse.
type AdminConnectionList struct {
	Connections []AdminConnectionInfo `json:"connections"`
	Total       int                   `json:"total"`
}

// AdminAuthKeyCreate mirrors the admin authKeyCreateResponse.
type AdminAuthKeyCreate struct {
	Name    string   `json:"name"`
	Key     string   `json:"key"`
	Roles   []string `json:"roles"`
	Warning string   `json:"warning"`
}

// AdminKeySummary mirrors auth.APIKeySummary.
type AdminKeySummary struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// AdminAuthKeyList mirrors the admin authKeyListResponse.
type AdminAuthKeyList struct {
	Keys  []AdminKeySummary `json:"keys"`
	Total int               `json:"total"`
}

// AdminAuditEventList mirrors the admin auditEventResponse.
type AdminAuditEventList struct {
	Data    []audit.Event `json:"data"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"per_page"`
}

// AdminAuditStats mirrors the admin auditStatsResponse.
type AdminAuditStats struct {
	Total    int `json:"total"`
	Success  int `json:"success"`
	Failures int `json:"failures"`
}

// AdminConfigImport mirrors the admin configImportResponse.
type AdminConfigImport struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// AdminConfigHistory mirrors the admin configHistoryResponse.
type AdminConfigHistory struct {
	Revisions []configstore.Revision `json:"revisions"`
	Total     int                    `json:"total"`
}

// AdminProblem mirrors the admin problemDetail (RFC 9457).
type AdminProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// AdminStatus mirrors the admin statusResponse.
type AdminStatus struct {
	Status string `json:"status"`
}

// --- AdminClient ---

// AdminClient is an HTTP client for the admin API.
type AdminClient struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewAdminClient creates a new AdminClient.
func NewAdminClient(baseURL, apiKey string) *AdminClient {
	return &AdminClient{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// doRequest performs an HTTP request with auth header.
func (c *AdminClient) doRequest(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Client.Do(req)
}

func (c *AdminClient) get(path string) (*http.Response, error) {
	return c.doRequest(http.MethodGet, path, nil, "")
}

func (c *AdminClient) postJSON(path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling body: %w", err)
	}
	return c.doRequest(http.MethodPost, path, bytes.NewReader(data), "application/json")
}

func (c *AdminClient) postYAML(path string, yamlBody string) (*http.Response, error) {
	return c.doRequest(http.MethodPost, path, bytes.NewReader([]byte(yamlBody)), "application/x-yaml")
}

func (c *AdminClient) putJSON(path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling body: %w", err)
	}
	return c.doRequest(http.MethodPut, path, bytes.NewReader(data), "application/json")
}

func (c *AdminClient) delete(path string) (*http.Response, error) {
	return c.doRequest(http.MethodDelete, path, nil, "")
}

// --- System endpoints ---

// SystemInfo calls GET /api/v1/admin/system/info.
func (c *AdminClient) SystemInfo() (*AdminSystemInfo, int, error) {
	resp, err := c.get("/api/v1/admin/system/info")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminSystemInfo
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// ListTools calls GET /api/v1/admin/tools.
func (c *AdminClient) ListTools() (*AdminToolList, int, error) {
	resp, err := c.get("/api/v1/admin/tools")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminToolList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// ListConnections calls GET /api/v1/admin/connections.
func (c *AdminClient) ListConnections() (*AdminConnectionList, int, error) {
	resp, err := c.get("/api/v1/admin/connections")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminConnectionList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// --- Config endpoints ---

// GetConfig calls GET /api/v1/admin/config.
func (c *AdminClient) GetConfig() (map[string]any, int, error) {
	resp, err := c.get("/api/v1/admin/config")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return result, resp.StatusCode, nil
}

// GetConfigMode calls GET /api/v1/admin/config/mode.
func (c *AdminClient) GetConfigMode() (*AdminConfigMode, int, error) {
	resp, err := c.get("/api/v1/admin/config/mode")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminConfigMode
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// ExportConfig calls GET /api/v1/admin/config/export with optional secrets param.
func (c *AdminClient) ExportConfig(secrets bool) (string, int, error) {
	path := "/api/v1/admin/config/export"
	if secrets {
		path += "?secrets=true"
	}
	resp, err := c.get(path)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}

// ImportConfig calls POST /api/v1/admin/config/import.
func (c *AdminClient) ImportConfig(yamlBody, comment string) (*AdminConfigImport, int, error) {
	path := "/api/v1/admin/config/import"
	if comment != "" {
		path += "?comment=" + comment
	}
	resp, err := c.postYAML(path, yamlBody)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminConfigImport
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// ConfigHistory calls GET /api/v1/admin/config/history.
func (c *AdminClient) ConfigHistory() (*AdminConfigHistory, int, error) {
	resp, err := c.get("/api/v1/admin/config/history")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminConfigHistory
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// --- Persona endpoints ---

// ListPersonas calls GET /api/v1/admin/personas.
func (c *AdminClient) ListPersonas() (*AdminPersonaList, int, error) {
	resp, err := c.get("/api/v1/admin/personas")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminPersonaList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// GetPersona calls GET /api/v1/admin/personas/{name}.
func (c *AdminClient) GetPersona(name string) (*AdminPersonaDetail, int, error) {
	resp, err := c.get("/api/v1/admin/personas/" + name)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, resp.StatusCode, nil
	}
	var result AdminPersonaDetail
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// PersonaCreateRequest is the request body for creating/updating a persona.
type PersonaCreateRequest struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	AllowTools  []string `json:"allow_tools"`
	DenyTools   []string `json:"deny_tools,omitempty"`
	Priority    int      `json:"priority,omitempty"`
}

// CreatePersona calls POST /api/v1/admin/personas.
func (c *AdminClient) CreatePersona(req PersonaCreateRequest) (*AdminPersonaDetail, int, error) {
	resp, err := c.postJSON("/api/v1/admin/personas", req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminPersonaDetail
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// UpdatePersona calls PUT /api/v1/admin/personas/{name}.
func (c *AdminClient) UpdatePersona(name string, req PersonaCreateRequest) (*AdminPersonaDetail, int, error) {
	resp, err := c.putJSON("/api/v1/admin/personas/"+name, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminPersonaDetail
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// DeletePersona calls DELETE /api/v1/admin/personas/{name}.
func (c *AdminClient) DeletePersona(name string) (int, error) {
	resp, err := c.delete("/api/v1/admin/personas/" + name)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// --- Auth key endpoints ---

// ListAuthKeys calls GET /api/v1/admin/auth/keys.
func (c *AdminClient) ListAuthKeys() (*AdminAuthKeyList, int, error) {
	resp, err := c.get("/api/v1/admin/auth/keys")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AdminAuthKeyList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// AuthKeyCreateRequest is the request body for creating an API key.
type AuthKeyCreateRequest struct {
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

// CreateAuthKey calls POST /api/v1/admin/auth/keys.
func (c *AdminClient) CreateAuthKey(name string, roles []string) (*AdminAuthKeyCreate, int, error) {
	resp, err := c.postJSON("/api/v1/admin/auth/keys", AuthKeyCreateRequest{Name: name, Roles: roles})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminAuthKeyCreate
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// DeleteAuthKey calls DELETE /api/v1/admin/auth/keys/{name}.
func (c *AdminClient) DeleteAuthKey(name string) (int, error) {
	resp, err := c.delete("/api/v1/admin/auth/keys/" + name)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// --- Audit endpoints ---

// ListAuditEvents calls GET /api/v1/admin/audit/events with query params.
func (c *AdminClient) ListAuditEvents(params string) (*AdminAuditEventList, int, error) {
	path := "/api/v1/admin/audit/events"
	if params != "" {
		path += "?" + params
	}
	resp, err := c.get(path)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminAuditEventList
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// GetAuditEvent calls GET /api/v1/admin/audit/events/{id}.
func (c *AdminClient) GetAuditEvent(id string) (int, error) {
	resp, err := c.get("/api/v1/admin/audit/events/" + id)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// GetAuditStats calls GET /api/v1/admin/audit/stats.
func (c *AdminClient) GetAuditStats() (*AdminAuditStats, int, error) {
	resp, err := c.get("/api/v1/admin/audit/stats")
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, nil
	}
	var result AdminAuditStats
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, err
	}
	return &result, resp.StatusCode, nil
}

// RawGet performs a raw GET and returns status code and response body bytes.
func (c *AdminClient) RawGet(path string) (int, []byte, error) {
	resp, err := c.get(path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

// RawPost performs a raw POST with JSON body and returns status code.
func (c *AdminClient) RawPost(path string, body any) (int, error) {
	resp, err := c.postJSON(path, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// RawDelete performs a raw DELETE and returns status code.
func (c *AdminClient) RawDelete(path string) (int, error) {
	resp, err := c.delete(path)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// RawPut performs a raw PUT with JSON body and returns status code.
func (c *AdminClient) RawPut(path string, body any) (int, error) {
	resp, err := c.putJSON(path, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// --- PostgreSQL container ---

// StartPostgres starts a PostgreSQL testcontainer and returns its DSN.
// The container is automatically terminated when the test completes.
func StartPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("getting postgres connection string: %v", err)
	}
	return dsn
}

// --- Config builders ---

// baseAdminConfig returns a config with the common admin e2e settings.
func baseAdminConfig() *platform.Config {
	return &platform.Config{
		APIVersion: "v2",
		Server: platform.ServerConfig{
			Name:      "e2e-admin-test",
			Transport: "stdio",
		},
		Auth: platform.AuthConfig{
			APIKeys: platform.APIKeyAuthConfig{
				Enabled: true,
				Keys: []platform.APIKeyDef{
					{Key: AdminAPIKey, Name: "admin", Roles: []string{"admin"}},
					{Key: AnalystAPIKey, Name: "analyst", Roles: []string{"analyst"}},
				},
			},
		},
		Personas: platform.PersonasConfig{
			Definitions: map[string]platform.PersonaDef{
				"admin": {
					DisplayName: "Administrator",
					Roles:       []string{"admin"},
					Tools:       platform.ToolRulesDef{Allow: []string{"*"}},
					Priority:    100,
				},
				"analyst": {
					DisplayName: "Data Analyst",
					Roles:       []string{"analyst"},
					Tools: platform.ToolRulesDef{
						Allow: []string{"trino_*", "datahub_*"},
					},
				},
			},
			DefaultPersona: "analyst",
		},
		Admin: platform.AdminConfig{
			Enabled:    true,
			Persona:    "admin",
			PathPrefix: "/api/v1/admin",
		},
		Audit: platform.AuditConfig{
			Enabled:      true,
			LogToolCalls: true,
		},
		Knowledge: platform.KnowledgeConfig{
			Enabled: true,
		},
	}
}

// StandaloneAdminConfig returns a config for standalone mode (no database).
func StandaloneAdminConfig() *platform.Config {
	return baseAdminConfig()
}

// FileDBAdminConfig returns a config for file+DB mode.
func FileDBAdminConfig(pgDSN string) *platform.Config {
	cfg := baseAdminConfig()
	cfg.Database = platform.DatabaseConfig{DSN: pgDSN}
	// config_store defaults to file mode
	return cfg
}

// BootstrapDBAdminConfig returns a config for bootstrap+DB mode.
func BootstrapDBAdminConfig(pgDSN string) *platform.Config {
	cfg := baseAdminConfig()
	cfg.Database = platform.DatabaseConfig{DSN: pgDSN}
	cfg.ConfigStore = platform.ConfigStoreConfig{Mode: "database"}
	return cfg
}

// --- Handler builder ---

// BuildAdminHandler replicates the production admin handler wiring from main.go.
func BuildAdminHandler(p *platform.Platform) http.Handler {
	platAuth := admin.NewPlatformAuthenticator(
		p.Authenticator(),
		p.Config().Admin.Persona,
		p.PersonaRegistry(),
	)

	deps := admin.Deps{
		Config:            p.Config(),
		ConfigStore:       p.ConfigStore(),
		PersonaRegistry:   p.PersonaRegistry(),
		ToolkitRegistry:   p.ToolkitRegistry(),
		MCPServer:         p.MCPServer(),
		DatabaseAvailable: p.Config().Database.DSN != "",
		PlatformTools:     p.PlatformTools(),
	}

	if p.AuditStore() != nil {
		deps.AuditQuerier = p.AuditStore()
	}

	if p.KnowledgeInsightStore() != nil {
		deps.Knowledge = admin.NewKnowledgeHandler(
			p.KnowledgeInsightStore(),
			p.KnowledgeChangesetStore(),
			p.KnowledgeDataHubWriter(),
		)
	}

	if p.APIKeyAuthenticator() != nil {
		deps.APIKeyManager = p.APIKeyAuthenticator()
	}

	return admin.NewHandler(deps, admin.RequirePersona(platAuth))
}

// --- DB helper ---

// AdminTestDB holds database resources for admin e2e tests.
type AdminTestDB struct {
	DB         *sql.DB
	AuditStore *auditpostgres.Store
}

// NewAdminTestDB opens a connection, drops init-script tables, and runs migrations.
func NewAdminTestDB(t *testing.T, dsn string) *AdminTestDB {
	t.Helper()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("pinging database: %v", err)
	}

	// Drop init-script tables so golang-migrate owns the schema
	initTables := []string{
		"oauth_refresh_tokens", "oauth_access_tokens",
		"oauth_authorization_codes", "oauth_clients",
		"audit_logs", "schema_migrations",
	}
	for _, tbl := range initTables {
		//nolint:gosec // test-only, table names are hardcoded constants
		if _, err := db.Exec("DROP TABLE IF EXISTS " + tbl + " CASCADE"); err != nil {
			t.Fatalf("dropping init table %s: %v", tbl, err)
		}
	}

	if err := migrate.Run(db); err != nil {
		t.Fatalf("running migrations: %v", err)
	}

	return &AdminTestDB{
		DB:         db,
		AuditStore: auditpostgres.New(db, auditpostgres.Config{}),
	}
}

// TruncateAdminTables removes all rows from admin-relevant tables.
func (a *AdminTestDB) TruncateAdminTables(t *testing.T) {
	t.Helper()

	tables := "audit_logs, config_versions"
	_, err := a.DB.Exec("TRUNCATE " + tables)
	if err != nil {
		t.Fatalf("truncating admin tables: %v", err)
	}
}

// InsertAuditEvent inserts an audit event directly into the database.
func (a *AdminTestDB) InsertAuditEvent(t *testing.T, event audit.Event) {
	t.Helper()

	if err := a.AuditStore.Log(context.Background(), event); err != nil {
		t.Fatalf("inserting audit event: %v", err)
	}
}

// Close closes the database connection.
func (a *AdminTestDB) Close() error {
	return a.DB.Close()
}
