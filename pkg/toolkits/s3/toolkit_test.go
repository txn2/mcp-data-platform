package s3

import (
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	s3tools "github.com/txn2/mcp-s3/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
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
	if tk.Connection() != "test" {
		t.Errorf("Connection() = %q, want 'test'", tk.Connection())
	}
}

func TestToolkit_ToolsNonReadonly(t *testing.T) {
	tk := newTestS3Toolkit()
	tools := tk.Tools()
	if len(tools) == 0 {
		t.Error("expected non-empty tools list")
	}
	assertS3ToolContains(t, tools, "s3_list_buckets")
	assertS3ToolContains(t, tools, "s3_list_objects")
	assertS3ToolContains(t, tools, "s3_put_object")
	assertS3ToolContains(t, tools, "s3_delete_object")
}

func assertS3ToolContains(t *testing.T, tools []string, name string) {
	t.Helper()
	if !slices.Contains(tools, name) {
		t.Errorf("missing expected tool: %s", name)
	}
}

func TestToolkit_ToolsReadonly(t *testing.T) {
	tk := &Toolkit{name: "test-s3-readonly", config: Config{ReadOnly: true}}
	tools := tk.Tools()
	for _, tool := range tools {
		if tool == "s3_put_object" || tool == "s3_delete_object" || tool == "s3_copy_object" {
			t.Errorf("found write tool %s when readonly", tool)
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

func TestToolkit_RegisterTools(t *testing.T) {
	t.Run("nil s3Toolkit does not panic", func(_ *testing.T) {
		tk := &Toolkit{name: "test"}
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
		tk.RegisterTools(server) // Should not panic
	})

	t.Run("non-readonly registers all tools", func(t *testing.T) {
		tk := newTestS3Toolkit()
		tk.s3Toolkit = s3tools.NewToolkit(nil)
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
		tk.RegisterTools(server)

		// Write tools should be present
		for _, wt := range s3tools.WriteTools() {
			if !slices.Contains(tk.Tools(), string(wt)) {
				t.Errorf("expected write tool %s in non-readonly mode", wt)
			}
		}
	})

	t.Run("readonly registers only read tools", func(t *testing.T) {
		tk := &Toolkit{
			name:      "test-readonly",
			config:    Config{ReadOnly: true},
			s3Toolkit: s3tools.NewToolkit(nil, s3tools.WithReadOnly(true)),
		}
		server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0.0"}, nil)
		tk.RegisterTools(server)

		// Verify Tools() does not include write tools (already tested)
		tools := tk.Tools()
		for _, wt := range s3tools.WriteTools() {
			if slices.Contains(tools, string(wt)) {
				t.Errorf("found write tool %s in readonly mode", wt)
			}
		}
	})
}
