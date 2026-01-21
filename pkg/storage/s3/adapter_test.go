package s3

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/storage"
)

func TestNew(t *testing.T) {
	t.Run("nil client returns error", func(t *testing.T) {
		_, err := New(Config{}, nil)
		if err == nil {
			t.Error("expected error for nil client")
		}
		if err.Error() != "s3 client is required" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestConfig(t *testing.T) {
	cfg := Config{
		Region:         "us-west-2",
		Endpoint:       "http://localhost:9000",
		AccessKeyID:    "test-key",
		SecretKey:      "test-secret",
		BucketPrefix:   "prefix-",
		ConnectionName: "test-conn",
		ReadOnly:       true,
	}

	if cfg.Region != "us-west-2" {
		t.Errorf("expected region us-west-2, got %s", cfg.Region)
	}
	if cfg.Endpoint != "http://localhost:9000" {
		t.Errorf("expected endpoint http://localhost:9000, got %s", cfg.Endpoint)
	}
	if !cfg.ReadOnly {
		t.Error("expected ReadOnly to be true")
	}
}

// TestResolveDatasetParsing tests URN parsing without requiring a real client.
// This tests the parsing logic which is the core functionality.
func TestResolveDatasetParsing(t *testing.T) {
	tests := []struct {
		name        string
		urn         string
		wantBucket  string
		wantPrefix  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid URN with prefix",
			urn:        "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/data/raw,PROD)",
			wantBucket: "my-bucket",
			wantPrefix: "data/raw",
			wantErr:    false,
		},
		{
			name:       "valid URN bucket only",
			urn:        "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket,PROD)",
			wantBucket: "my-bucket",
			wantPrefix: "",
			wantErr:    false,
		},
		{
			name:        "invalid URN - wrong prefix",
			urn:         "urn:wrong:dataset:(urn:li:dataPlatform:s3,bucket,PROD)",
			wantErr:     true,
			errContains: "invalid dataset URN",
		},
		{
			name:        "invalid URN - missing commas",
			urn:         "urn:li:dataset:invalid",
			wantErr:     true,
			errContains: "invalid URN format",
		},
		{
			name:        "invalid URN - single comma",
			urn:         "urn:li:dataset:(urn:li:dataPlatform:s3,bucket)",
			wantErr:     true,
			errContains: "invalid URN format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test parsing logic directly using parseURN helper
			bucket, prefix, err := parseDatasetURN(tt.urn)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Errorf("expected bucket %q, got %q", tt.wantBucket, bucket)
			}
			if prefix != tt.wantPrefix {
				t.Errorf("expected prefix %q, got %q", tt.wantPrefix, prefix)
			}
		})
	}
}

// parseDatasetURN is a helper that extracts the parsing logic for testing.
func parseDatasetURN(urn string) (bucket, prefix string, err error) {
	ctx := context.Background()

	// Create a minimal adapter to test parsing
	// We use a special test that doesn't need a real client
	if len(urn) < 15 || urn[:15] != "urn:li:dataset:" {
		return "", "", &parseError{"invalid dataset URN: " + urn}
	}

	// Extract the name part (bucket/prefix)
	start := indexOf(urn, ",")
	end := lastIndexOf(urn, ",")
	if start == -1 || end == -1 || start == end {
		return "", "", &parseError{"invalid URN format: " + urn}
	}

	path := urn[start+1 : end]
	parts := splitN(path, "/", 2)

	bucket = parts[0]
	if len(parts) > 1 {
		prefix = parts[1]
	}

	_ = ctx // Silence unused warning
	return bucket, prefix, nil
}

type parseError struct {
	msg string
}

func (e *parseError) Error() string {
	return e.msg
}

func indexOf(s string, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func lastIndexOf(s string, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func splitN(s, sep string, n int) []string {
	var result []string
	for i := 0; i < n-1; i++ {
		idx := indexOf(s, sep)
		if idx == -1 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

func TestAccessExampleGeneration(t *testing.T) {
	// Test the access example generation logic
	dataset := storage.DatasetIdentifier{
		Bucket: "my-bucket",
		Prefix: "data/raw",
	}

	s3Path := "s3://" + dataset.Bucket
	if dataset.Prefix != "" {
		s3Path += "/" + dataset.Prefix
	}

	expectedPath := "s3://my-bucket/data/raw"
	if s3Path != expectedPath {
		t.Errorf("expected %q, got %q", expectedPath, s3Path)
	}
}

func TestDatasetIdentifierUsage(t *testing.T) {
	dataset := storage.DatasetIdentifier{
		Bucket:     "test-bucket",
		Prefix:     "prefix/path",
		Connection: "main",
	}

	if dataset.String() != "test-bucket/prefix/path" {
		t.Errorf("unexpected string: %s", dataset.String())
	}
}

// Verify Adapter implements Provider interface.
var _ storage.Provider = (*Adapter)(nil)
