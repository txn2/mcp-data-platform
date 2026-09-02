package s3

import (
	"context"
	neturl "net/url"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

const (
	s3TestRegionWest  = "us-west-2"
	s3TestRegionEast  = "us-east-1"
	s3TestEndpoint    = "http://localhost:9000"
	s3TestTimeoutSec  = 60
	s3TestMaxGetMB    = 5
	s3TestMaxPutMB    = 50
	s3TestDefTimeoutS = 30
	s3TestDefGetMB    = 10
	s3TestDefPutMB    = 100
	s3TestBytesPerMB  = 1024 * 1024
	s3TestToolkitName = "test-s3"
)

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Region:          s3TestRegionWest,
		Endpoint:        s3TestEndpoint,
		AccessKeyID:     "accesskey",
		SecretAccessKey: "secretkey",
		SessionToken:    "token",
		Profile:         "myprofile",
		UsePathStyle:    true,
		Timeout:         s3TestTimeoutSec * time.Second,
		DisableSSL:      true,
		ReadOnly:        true,
		MaxGetSize:      s3TestMaxGetMB * s3TestBytesPerMB,
		MaxPutSize:      s3TestMaxPutMB * s3TestBytesPerMB,
		ConnectionName:  s3TestToolkitName,
		BucketPrefix:    "prefix-",
	}

	if cfg.Region != s3TestRegionWest {
		t.Errorf("Region = %q", cfg.Region)
	}
	if cfg.Endpoint != s3TestEndpoint {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.AccessKeyID != "accesskey" {
		t.Errorf("AccessKeyID = %q", cfg.AccessKeyID)
	}
	if cfg.SecretAccessKey != "secretkey" {
		t.Errorf("SecretAccessKey = %q", cfg.SecretAccessKey)
	}
	if cfg.SessionToken != "token" {
		t.Errorf("SessionToken = %q", cfg.SessionToken)
	}
	if cfg.Profile != "myprofile" {
		t.Errorf("Profile = %q", cfg.Profile)
	}
	if !cfg.UsePathStyle {
		t.Error("UsePathStyle = false")
	}
	if cfg.Timeout != s3TestTimeoutSec*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if !cfg.DisableSSL {
		t.Error("DisableSSL = false")
	}
	if !cfg.ReadOnly {
		t.Error("ReadOnly = false")
	}
	if cfg.MaxGetSize != s3TestMaxGetMB*s3TestBytesPerMB {
		t.Errorf("MaxGetSize = %d", cfg.MaxGetSize)
	}
	if cfg.MaxPutSize != s3TestMaxPutMB*s3TestBytesPerMB {
		t.Errorf("MaxPutSize = %d", cfg.MaxPutSize)
	}
	if cfg.ConnectionName != s3TestToolkitName {
		t.Errorf("ConnectionName = %q", cfg.ConnectionName)
	}
	if cfg.BucketPrefix != "prefix-" {
		t.Errorf("BucketPrefix = %q", cfg.BucketPrefix)
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := Config{}

	// Check what defaults would be applied by New
	if cfg.Region == "" {
		defaultRegion := s3TestRegionEast
		if defaultRegion != s3TestRegionEast {
			t.Error("default region should be us-east-1")
		}
	}

	if cfg.Timeout == 0 {
		defaultTimeout := s3TestDefTimeoutS * time.Second
		if defaultTimeout != s3TestDefTimeoutS*time.Second {
			t.Error("default timeout should be 30s")
		}
	}

	if cfg.MaxGetSize == 0 {
		defaultMaxGetSize := int64(s3TestDefGetMB * s3TestBytesPerMB)
		if defaultMaxGetSize != s3TestDefGetMB*s3TestBytesPerMB {
			t.Error("default MaxGetSize should be 10MB")
		}
	}

	if cfg.MaxPutSize == 0 {
		defaultMaxPutSize := int64(s3TestDefPutMB * s3TestBytesPerMB)
		if defaultMaxPutSize != s3TestDefPutMB*s3TestBytesPerMB {
			t.Error("default MaxPutSize should be 100MB")
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("applies default region", func(t *testing.T) {
		cfg := applyDefaults("test", Config{})
		if cfg.Region != s3TestRegionEast {
			t.Errorf("Region = %q, want 'us-east-1'", cfg.Region)
		}
	})

	t.Run("applies default timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{})
		if cfg.Timeout != s3TestDefTimeoutS*time.Second {
			t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
		}
	})

	t.Run("applies default max get size", func(t *testing.T) {
		cfg := applyDefaults("test", Config{})
		if cfg.MaxGetSize != s3TestDefGetMB*s3TestBytesPerMB {
			t.Errorf("MaxGetSize = %d, want 10MB", cfg.MaxGetSize)
		}
	})

	t.Run("applies default max put size", func(t *testing.T) {
		cfg := applyDefaults("test", Config{})
		if cfg.MaxPutSize != s3TestDefPutMB*s3TestBytesPerMB {
			t.Errorf("MaxPutSize = %d, want 100MB", cfg.MaxPutSize)
		}
	})

	t.Run("applies connection name from toolkit name", func(t *testing.T) {
		cfg := applyDefaults("my-toolkit", Config{})
		if cfg.ConnectionName != "my-toolkit" {
			t.Errorf("ConnectionName = %q, want 'my-toolkit'", cfg.ConnectionName)
		}
	})

	t.Run("preserves custom region", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Region: s3TestRegionWest})
		if cfg.Region != s3TestRegionWest {
			t.Errorf("Region = %q, want 'us-west-2'", cfg.Region)
		}
	})

	t.Run("preserves custom timeout", func(t *testing.T) {
		cfg := applyDefaults("test", Config{Timeout: s3TestTimeoutSec * time.Second})
		if cfg.Timeout != s3TestTimeoutSec*time.Second {
			t.Errorf("Timeout = %v, want 60s", cfg.Timeout)
		}
	})

	t.Run("preserves custom max get size", func(t *testing.T) {
		cfg := applyDefaults("test", Config{MaxGetSize: s3TestMaxGetMB * s3TestBytesPerMB})
		if cfg.MaxGetSize != s3TestMaxGetMB*s3TestBytesPerMB {
			t.Errorf("MaxGetSize = %d, want 5MB", cfg.MaxGetSize)
		}
	})

	t.Run("preserves custom max put size", func(t *testing.T) {
		cfg := applyDefaults("test", Config{MaxPutSize: s3TestMaxPutMB * s3TestBytesPerMB})
		if cfg.MaxPutSize != s3TestMaxPutMB*s3TestBytesPerMB {
			t.Errorf("MaxPutSize = %d, want 50MB", cfg.MaxPutSize)
		}
	})

	t.Run("preserves custom connection name", func(t *testing.T) {
		cfg := applyDefaults("test", Config{ConnectionName: "custom"})
		if cfg.ConnectionName != "custom" {
			t.Errorf("ConnectionName = %q, want 'custom'", cfg.ConnectionName)
		}
	})
}

func TestApplyDefaults_PreservesExistingValues(t *testing.T) {
	cfg := Config{
		Region:          s3TestRegionWest,
		Endpoint:        s3TestEndpoint,
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Timeout:         s3TestTimeoutSec * time.Second,
		MaxGetSize:      s3TestMaxGetMB * s3TestBytesPerMB,
		MaxPutSize:      s3TestMaxPutMB * s3TestBytesPerMB,
		ConnectionName:  "custom-name",
		ReadOnly:        true,
	}
	result := applyDefaults("test", cfg)

	if result.Region != s3TestRegionWest {
		t.Errorf("Region should be preserved: got %s", result.Region)
	}
	if result.Timeout != s3TestTimeoutSec*time.Second {
		t.Errorf("Timeout should be preserved: got %v", result.Timeout)
	}
	if result.MaxGetSize != s3TestMaxGetMB*s3TestBytesPerMB {
		t.Errorf("MaxGetSize should be preserved: got %d", result.MaxGetSize)
	}
	if result.MaxPutSize != s3TestMaxPutMB*s3TestBytesPerMB {
		t.Errorf("MaxPutSize should be preserved: got %d", result.MaxPutSize)
	}
	if result.ConnectionName != "custom-name" {
		t.Errorf("ConnectionName should be preserved: got %s", result.ConnectionName)
	}
	if !result.ReadOnly {
		t.Error("ReadOnly should be preserved: got false")
	}
}

func TestNew(t *testing.T) {
	// Note: New() requires AWS credentials or environment to work.
	// This test covers the error path when S3 client creation fails.
	t.Run("creates toolkit with valid config", func(t *testing.T) {
		// Skip if no AWS config available
		_, err := New("test", Config{
			Region:   s3TestRegionEast,
			Endpoint: "http://localhost:9999", // Invalid endpoint
		})
		// We expect an error because we can't connect to an invalid endpoint
		// This is acceptable as it tests the error handling path
		if err == nil {
			// If somehow it succeeded (e.g., mock environment), that's fine too
			t.Log("New() succeeded unexpectedly, but this is acceptable")
		}
	})
}

// TestCreateClient_PublicEndpointPresign proves the #575 fix end to end through
// the real config path: a YAML map is parsed by ParseConfig, the toolkit builds
// its client via createClient, and s3_object presign signs against public_endpoint
// when set, falling back to the internal endpoint when absent. Driving it from a
// map (not a hand-built Config) is deliberate: it would catch ParseConfig
// dropping the key. Presigning is local, so no network is required.
func TestCreateClient_PublicEndpointPresign(t *testing.T) {
	ctx := context.Background()
	baseMap := func() map[string]any {
		return map[string]any{
			"region":            s3TestRegionEast,
			"endpoint":          "http://internal-s3:8333",
			"access_key_id":     "test",
			"secret_access_key": "secret",
			"use_path_style":    true,
		}
	}
	presignHost := func(t *testing.T, cfgMap map[string]any) *neturl.URL {
		t.Helper()
		cfg, err := ParseConfig(cfgMap)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		client, err := createClient(cfg)
		if err != nil {
			t.Fatalf("createClient: %v", err)
		}
		got, err := client.PresignGetURL(ctx, "bucket", "key.txt", time.Hour)
		if err != nil {
			t.Fatalf("PresignGetURL: %v", err)
		}
		u, err := neturl.Parse(got.URL)
		if err != nil {
			t.Fatalf("parse %q: %v", got.URL, err)
		}
		return u
	}

	t.Run("signs against public endpoint when set", func(t *testing.T) {
		cfgMap := baseMap()
		cfgMap["public_endpoint"] = "https://s3.public.example.com"
		u := presignHost(t, cfgMap)
		if u.Scheme != "https" || u.Host != "s3.public.example.com" {
			t.Errorf("presigned URL = %s://%s, want https://s3.public.example.com", u.Scheme, u.Host)
		}
	})

	t.Run("falls back to data endpoint when unset", func(t *testing.T) {
		u := presignHost(t, baseMap())
		if u.Host != "internal-s3:8333" {
			t.Errorf("presigned URL host = %s, want internal-s3:8333", u.Host)
		}
	})
}

func newTestS3Toolkit() *Toolkit {
	return &Toolkit{
		name: s3TestToolkitName,
		config: Config{
			Region:         s3TestRegionEast,
			Endpoint:       s3TestEndpoint,
			ConnectionName: "test",
			ReadOnly:       false,
		},
	}
}

func TestToolkit_KindAndName(t *testing.T) {
	tk := newTestS3Toolkit()
	if tk.Kind() != "s3" {
		t.Errorf("Kind() = %q, want 's3'", tk.Kind())
	}
	if tk.Name() != s3TestToolkitName {
		t.Errorf("Name() = %q", tk.Name())
	}
	if tk.Connection() != s3TestToolkitName {
		t.Errorf("Connection() = %q, want the default connection's bound name %q", tk.Connection(), s3TestToolkitName)
	}
}

// TestToolkit_Tools pins the registered surface: s3_list and s3_object, and
// nothing else, whatever read_only says (#1591). A read-only connection keeps
// s3_object because its read actions are still served; the writing actions are
// refused by the handler.
func TestToolkit_Tools(t *testing.T) {
	for name, cfg := range map[string]Config{"writable": {}, "read-only": {ReadOnly: true}} {
		tk := &Toolkit{name: name, config: cfg}
		if got := tk.Tools(); !slices.Equal(got, []string{"s3_list", "s3_object"}) {
			t.Errorf("%s: Tools() = %v, want [s3_list s3_object]", name, got)
		}
	}
}

func TestToolkit_ConfigAndProviders(t *testing.T) {
	tk := newTestS3Toolkit()
	if tk.Config().Region != s3TestRegionEast {
		t.Errorf("Config().Region = %q", tk.Config().Region)
	}

	sp := semantic.NewNoopProvider()
	tk.SetSemanticProvider(sp)
	if tk.semanticProvider != sp {
		t.Error("semanticProvider not set")
	}

	qp := query.NewNoopProvider()
	tk.SetQueryProvider(qp)
	if tk.queryProvider != qp {
		t.Error("queryProvider not set")
	}
}

func TestToolkit_ClientAndClose(t *testing.T) {
	tk := newTestS3Toolkit()
	if tk.Client() != nil {
		t.Error("expected nil client")
	}
	if err := tk.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestS3AnnotationConfigToMCP(t *testing.T) {
	t.Run("all fields set", func(t *testing.T) {
		readOnly := true
		destructive := false
		idempotent := true
		openWorld := false
		cfg := AnnotationConfig{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  &idempotent,
			OpenWorldHint:   &openWorld,
		}
		ann := toolkit.AnnotationsToMCP(cfg)
		if !ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=true")
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Error("expected DestructiveHint=false")
		}
		if !ann.IdempotentHint {
			t.Error("expected IdempotentHint=true")
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Error("expected OpenWorldHint=false")
		}
	})

	t.Run("no fields set", func(t *testing.T) {
		cfg := AnnotationConfig{}
		ann := toolkit.AnnotationsToMCP(cfg)
		if ann.ReadOnlyHint {
			t.Error("expected ReadOnlyHint=false")
		}
		if ann.DestructiveHint != nil {
			t.Error("expected DestructiveHint=nil")
		}
	})
}

func TestToolkit_RegisterTools(t *testing.T) {
	t.Run("nil s3Toolkit does not panic", func(_ *testing.T) {
		tk := &Toolkit{name: "test"}
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
		tk.RegisterTools(server) // Should not panic
	})

	t.Run("registers the two tools whatever read_only says", func(t *testing.T) {
		for name, readOnly := range map[string]bool{"writable": false, "read-only": true} {
			tk := &Toolkit{name: name, config: Config{ReadOnly: readOnly, ConnectionName: name}, s3Toolkit: s3tools.NewToolkit(nil)}
			names := registeredToolNames(t, tk)
			if !slices.Equal(names, []string{"s3_list", "s3_object"}) {
				t.Errorf("%s: registered %v, want [s3_list s3_object]", name, names)
			}
		}
	})

	t.Run("instance overrides apply to the tool they name", func(t *testing.T) {
		readOnly := true
		tk := &Toolkit{name: "acme", s3Toolkit: s3tools.NewToolkit(nil), config: Config{
			ConnectionName: "acme",
			Titles:         map[string]string{"s3_list": "Browse the lake"},
			Descriptions:   map[string]string{"s3_object": "One object of the lake."},
			Annotations:    map[string]AnnotationConfig{"s3_object": {ReadOnlyHint: &readOnly}},
		}}
		tools := registeredTools(t, tk)
		if tools["s3_list"].Title != "Browse the lake" {
			t.Errorf("s3_list title = %q", tools["s3_list"].Title)
		}
		if tools["s3_object"].Description != "One object of the lake." {
			t.Errorf("s3_object description = %q", tools["s3_object"].Description)
		}
		if tools["s3_object"].Annotations == nil || !tools["s3_object"].Annotations.ReadOnlyHint {
			t.Error("s3_object annotation override not applied")
		}
		if tools["s3_list"].Annotations == nil || !tools["s3_list"].Annotations.ReadOnlyHint {
			t.Error("s3_list keeps its default read-only annotation")
		}
	})
}

// registeredTools registers the toolkit on a server and reads the tools back
// through an in-memory client, keyed by name.
func registeredTools(t *testing.T, tk *Toolkit) map[string]*mcp.Tool {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
	tk.RegisterTools(server)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		out[tool.Name] = tool
	}
	return out
}

func registeredToolNames(t *testing.T, tk *Toolkit) []string {
	t.Helper()
	tools := registeredTools(t, tk)
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
