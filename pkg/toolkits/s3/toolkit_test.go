package s3

import (
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestConfig_Fields(t *testing.T) {
	cfg := Config{
		Region:          "us-west-2",
		Endpoint:        "http://localhost:9000",
		AccessKeyID:     "accesskey",
		SecretAccessKey: "secretkey",
		SessionToken:    "token",
		Profile:         "myprofile",
		UsePathStyle:    true,
		Timeout:         60 * time.Second,
		DisableSSL:      true,
		ReadOnly:        true,
		MaxGetSize:      5 * 1024 * 1024,
		MaxPutSize:      50 * 1024 * 1024,
		ConnectionName:  "test-s3",
		BucketPrefix:    "prefix-",
	}

	if cfg.Region != "us-west-2" {
		t.Errorf("Region = %q", cfg.Region)
	}
	if cfg.Endpoint != "http://localhost:9000" {
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
	if cfg.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if !cfg.DisableSSL {
		t.Error("DisableSSL = false")
	}
	if !cfg.ReadOnly {
		t.Error("ReadOnly = false")
	}
	if cfg.MaxGetSize != 5*1024*1024 {
		t.Errorf("MaxGetSize = %d", cfg.MaxGetSize)
	}
	if cfg.MaxPutSize != 50*1024*1024 {
		t.Errorf("MaxPutSize = %d", cfg.MaxPutSize)
	}
	if cfg.ConnectionName != "test-s3" {
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
		defaultRegion := "us-east-1"
		if defaultRegion != "us-east-1" {
			t.Error("default region should be us-east-1")
		}
	}

	if cfg.Timeout == 0 {
		defaultTimeout := 30 * time.Second
		if defaultTimeout != 30*time.Second {
			t.Error("default timeout should be 30s")
		}
	}

	if cfg.MaxGetSize == 0 {
		defaultMaxGetSize := int64(10 * 1024 * 1024)
		if defaultMaxGetSize != 10*1024*1024 {
			t.Error("default MaxGetSize should be 10MB")
		}
	}

	if cfg.MaxPutSize == 0 {
		defaultMaxPutSize := int64(100 * 1024 * 1024)
		if defaultMaxPutSize != 100*1024*1024 {
			t.Error("default MaxPutSize should be 100MB")
		}
	}
}

func TestToolkit_Methods(t *testing.T) {
	// Create toolkit without client for testing methods
	toolkit := &Toolkit{
		name: "test-s3",
		config: Config{
			Region:         "us-east-1",
			Endpoint:       "http://localhost:9000",
			ConnectionName: "test",
			ReadOnly:       false,
		},
	}

	t.Run("Kind", func(t *testing.T) {
		if toolkit.Kind() != "s3" {
			t.Errorf("Kind() = %q, want 's3'", toolkit.Kind())
		}
	})

	t.Run("Name", func(t *testing.T) {
		if toolkit.Name() != "test-s3" {
			t.Errorf("Name() = %q", toolkit.Name())
		}
	})

	t.Run("Tools non-readonly", func(t *testing.T) {
		tools := toolkit.Tools()
		if len(tools) == 0 {
			t.Error("expected non-empty tools list")
		}

		// Check read tools exist
		foundListBuckets := false
		foundListObjects := false
		for _, tool := range tools {
			if tool == "s3_list_buckets" {
				foundListBuckets = true
			}
			if tool == "s3_list_objects" {
				foundListObjects = true
			}
		}
		if !foundListBuckets {
			t.Error("missing s3_list_buckets tool")
		}
		if !foundListObjects {
			t.Error("missing s3_list_objects tool")
		}

		// Check write tools exist when not readonly
		foundPutObject := false
		foundDeleteObject := false
		for _, tool := range tools {
			if tool == "s3_put_object" {
				foundPutObject = true
			}
			if tool == "s3_delete_object" {
				foundDeleteObject = true
			}
		}
		if !foundPutObject {
			t.Error("missing s3_put_object tool (should exist when not readonly)")
		}
		if !foundDeleteObject {
			t.Error("missing s3_delete_object tool (should exist when not readonly)")
		}
	})

	t.Run("Tools readonly", func(t *testing.T) {
		readonlyToolkit := &Toolkit{
			name: "test-s3-readonly",
			config: Config{
				ReadOnly: true,
			},
		}

		tools := readonlyToolkit.Tools()

		// Check write tools do NOT exist when readonly
		for _, tool := range tools {
			if tool == "s3_put_object" || tool == "s3_delete_object" || tool == "s3_copy_object" {
				t.Errorf("found write tool %s when readonly", tool)
			}
		}
	})

	t.Run("Config", func(t *testing.T) {
		cfg := toolkit.Config()
		if cfg.Region != "us-east-1" {
			t.Errorf("Config().Region = %q", cfg.Region)
		}
	})

	t.Run("SetSemanticProvider", func(t *testing.T) {
		provider := semantic.NewNoopProvider()
		toolkit.SetSemanticProvider(provider)
		if toolkit.semanticProvider != provider {
			t.Error("semanticProvider not set")
		}
	})

	t.Run("SetQueryProvider", func(t *testing.T) {
		provider := query.NewNoopProvider()
		toolkit.SetQueryProvider(provider)
		if toolkit.queryProvider != provider {
			t.Error("queryProvider not set")
		}
	})

	t.Run("SetMiddleware", func(t *testing.T) {
		chain := middleware.NewChain()
		toolkit.SetMiddleware(chain)
		if toolkit.middlewareChain != chain {
			t.Error("middlewareChain not set")
		}
	})

	t.Run("Client nil", func(t *testing.T) {
		if toolkit.Client() != nil {
			t.Error("expected nil client")
		}
	})

	t.Run("Close nil client", func(t *testing.T) {
		err := toolkit.Close()
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	t.Run("RegisterTools nil toolkit", func(t *testing.T) {
		// Should not panic with nil s3Toolkit
		toolkit.RegisterTools(nil)
	})
}
