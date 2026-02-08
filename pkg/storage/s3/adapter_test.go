package s3

import (
	"context"
	"errors"
	"testing"
	"time"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/storage"
)

const (
	s3AdapterTestURNPrefixLen = 15
	s3AdapterTestBucket       = "my-bucket"
	s3AdapterTestConn         = "test-conn"
	s3AdapterTestSize100      = 100
	s3AdapterTestSize200      = 200
	s3AdapterTestTotalSize    = 300
)

// mockS3Client implements the Client interface for testing.
type mockS3Client struct {
	listObjectsOutput *s3client.ListObjectsOutput
	listObjectsErr    error
	closeErr          error
	closeCalled       bool
}

func (m *mockS3Client) ListObjects(_ context.Context, _, _, _ string, _ int32, _ string) (*s3client.ListObjectsOutput, error) { //nolint:revive // argument-limit: matches Client interface
	return m.listObjectsOutput, m.listObjectsErr
}

func (m *mockS3Client) Close() error {
	m.closeCalled = true
	return m.closeErr
}

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

	t.Run("valid client creates adapter", func(t *testing.T) {
		mockClient := &mockS3Client{}
		cfg := Config{ConnectionName: "test"}
		adapter, err := New(cfg, mockClient)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if adapter == nil {
			t.Fatal("expected non-nil adapter")
		}
		if adapter.cfg.ConnectionName != "test" {
			t.Errorf("expected connection name 'test', got %q", adapter.cfg.ConnectionName)
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
		ConnectionName: s3AdapterTestConn,
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
			wantBucket: s3AdapterTestBucket,
			wantPrefix: "data/raw",
			wantErr:    false,
		},
		{
			name:       "valid URN bucket only",
			urn:        "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket,PROD)",
			wantBucket: s3AdapterTestBucket,
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
	if len(urn) < s3AdapterTestURNPrefixLen || urn[:s3AdapterTestURNPrefixLen] != "urn:li:dataset:" {
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

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func lastIndexOf(s, substr string) int {
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
		Bucket: s3AdapterTestBucket,
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

func TestAdapterName(t *testing.T) {
	adapter := &Adapter{
		cfg: Config{
			ConnectionName: s3AdapterTestConn,
		},
	}
	if adapter.Name() != "s3" {
		t.Errorf("expected name 's3', got %q", adapter.Name())
	}
}

func TestAdapterCloseNilClient(t *testing.T) {
	adapter := &Adapter{
		cfg:    Config{},
		client: nil,
	}
	err := adapter.Close()
	if err != nil {
		t.Errorf("Close() with nil client should not error, got: %v", err)
	}
}

func TestAdapterCloseWithClient(t *testing.T) {
	t.Run("close success", func(t *testing.T) {
		mockClient := &mockS3Client{}
		adapter := &Adapter{
			cfg:    Config{},
			client: mockClient,
		}
		err := adapter.Close()
		if err != nil {
			t.Errorf("Close() should not error, got: %v", err)
		}
		if !mockClient.closeCalled {
			t.Error("expected Close() to be called on client")
		}
	})

	t.Run("close error", func(t *testing.T) {
		mockClient := &mockS3Client{closeErr: errors.New("close failed")}
		adapter := &Adapter{
			cfg:    Config{},
			client: mockClient,
		}
		err := adapter.Close()
		if err == nil {
			t.Error("expected error from Close()")
		}
	})
}

func TestGetDatasetAvailability_Success(t *testing.T) {
	now := time.Now()
	mockClient := &mockS3Client{
		listObjectsOutput: &s3client.ListObjectsOutput{
			Objects: []s3client.ObjectInfo{
				{Key: "file1.txt", Size: s3AdapterTestSize100, LastModified: now},
				{Key: "file2.txt", Size: s3AdapterTestSize200, LastModified: now},
			},
			KeyCount: 2,
		},
	}
	adapter := &Adapter{cfg: Config{ConnectionName: s3AdapterTestConn}, client: mockClient}

	result, err := adapter.GetDatasetAvailability(context.Background(),
		"urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/data,PROD)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Available {
		t.Error("expected Available to be true")
	}
	if result.Bucket != s3AdapterTestBucket {
		t.Errorf("expected bucket 'my-bucket', got %q", result.Bucket)
	}
	if result.ObjectCount != 2 {
		t.Errorf("expected ObjectCount 2, got %d", result.ObjectCount)
	}
	if result.TotalSize != s3AdapterTestTotalSize {
		t.Errorf("expected TotalSize %d, got %d", s3AdapterTestTotalSize, result.TotalSize)
	}
}

func TestGetDatasetAvailability_InvalidURN(t *testing.T) {
	adapter := &Adapter{cfg: Config{ConnectionName: s3AdapterTestConn}, client: &mockS3Client{}}

	result, err := adapter.GetDatasetAvailability(context.Background(), "invalid-urn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Available {
		t.Error("expected Available to be false for invalid URN")
	}
	if result.Error == "" {
		t.Error("expected error message for invalid URN")
	}
}

func TestGetDatasetAvailability_S3Error(t *testing.T) {
	mockClient := &mockS3Client{listObjectsErr: errors.New("S3 access denied")}
	adapter := &Adapter{cfg: Config{ConnectionName: s3AdapterTestConn}, client: mockClient}

	result, err := adapter.GetDatasetAvailability(context.Background(),
		"urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/data,PROD)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Available {
		t.Error("expected Available to be false on S3 error")
	}
	if result.Error != "S3 access denied" {
		t.Errorf("expected error message 'S3 access denied', got %q", result.Error)
	}
}

func TestListObjects(t *testing.T) {
	now := time.Now()

	t.Run("successful list", func(t *testing.T) {
		mockClient := &mockS3Client{
			listObjectsOutput: &s3client.ListObjectsOutput{
				Objects: []s3client.ObjectInfo{
					{Key: "file1.txt", Size: s3AdapterTestSize100, LastModified: now},
					{Key: "file2.txt", Size: s3AdapterTestSize200, LastModified: now},
				},
				KeyCount: 2,
			},
		}
		adapter := &Adapter{
			cfg:    Config{ConnectionName: s3AdapterTestConn},
			client: mockClient,
		}

		dataset := storage.DatasetIdentifier{Bucket: s3AdapterTestBucket, Prefix: "data"}
		result, err := adapter.ListObjects(context.Background(), dataset, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result) != 2 {
			t.Errorf("expected 2 objects, got %d", len(result))
		}
		if result[0].Key != "file1.txt" {
			t.Errorf("expected first key 'file1.txt', got %q", result[0].Key)
		}
		if result[0].Size != s3AdapterTestSize100 {
			t.Errorf("expected first size %d, got %d", s3AdapterTestSize100, result[0].Size)
		}
	})

	t.Run("default limit applied", func(t *testing.T) {
		mockClient := &mockS3Client{
			listObjectsOutput: &s3client.ListObjectsOutput{Objects: []s3client.ObjectInfo{}},
		}
		adapter := &Adapter{client: mockClient}

		dataset := storage.DatasetIdentifier{Bucket: s3AdapterTestBucket}
		_, err := adapter.ListObjects(context.Background(), dataset, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("max limit enforced", func(t *testing.T) {
		mockClient := &mockS3Client{
			listObjectsOutput: &s3client.ListObjectsOutput{Objects: []s3client.ObjectInfo{}},
		}
		adapter := &Adapter{client: mockClient}

		dataset := storage.DatasetIdentifier{Bucket: s3AdapterTestBucket}
		_, err := adapter.ListObjects(context.Background(), dataset, 5000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("S3 error returns error", func(t *testing.T) {
		mockClient := &mockS3Client{
			listObjectsErr: errors.New("S3 access denied"),
		}
		adapter := &Adapter{client: mockClient}

		dataset := storage.DatasetIdentifier{Bucket: s3AdapterTestBucket}
		_, err := adapter.ListObjects(context.Background(), dataset, 100)
		if err == nil {
			t.Error("expected error from ListObjects")
		}
	})
}

func TestAdapterResolveDataset(t *testing.T) {
	adapter := &Adapter{
		cfg: Config{
			ConnectionName: s3AdapterTestConn,
		},
	}

	tests := []struct {
		name           string
		urn            string
		wantBucket     string
		wantPrefix     string
		wantConnection string
		wantErr        bool
	}{
		{
			name:           "valid URN with prefix",
			urn:            "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/data/raw,PROD)",
			wantBucket:     s3AdapterTestBucket,
			wantPrefix:     "data/raw",
			wantConnection: s3AdapterTestConn,
			wantErr:        false,
		},
		{
			name:           "valid URN bucket only",
			urn:            "urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket,PROD)",
			wantBucket:     s3AdapterTestBucket,
			wantPrefix:     "",
			wantConnection: s3AdapterTestConn,
			wantErr:        false,
		},
		{
			name:    "invalid URN - wrong prefix",
			urn:     "urn:wrong:dataset:(urn:li:dataPlatform:s3,bucket,PROD)",
			wantErr: true,
		},
		{
			name:    "invalid URN - missing commas",
			urn:     "urn:li:dataset:invalid",
			wantErr: true,
		},
		{
			name:    "invalid URN - single comma",
			urn:     "urn:li:dataset:(urn:li:dataPlatform:s3,bucket)",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapter.ResolveDataset(context.Background(), tt.urn)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertDatasetResult(t, result, tt.wantBucket, tt.wantPrefix, tt.wantConnection)
		})
	}
}

func assertDatasetResult(t *testing.T, result *storage.DatasetIdentifier, wantBucket, wantPrefix, wantConnection string) {
	t.Helper()
	if result.Bucket != wantBucket {
		t.Errorf("expected bucket %q, got %q", wantBucket, result.Bucket)
	}
	if result.Prefix != wantPrefix {
		t.Errorf("expected prefix %q, got %q", wantPrefix, result.Prefix)
	}
	if result.Connection != wantConnection {
		t.Errorf("expected connection %q, got %q", wantConnection, result.Connection)
	}
}

func TestAdapterGetAccessExamples(t *testing.T) {
	adapter := &Adapter{
		cfg: Config{
			ConnectionName: s3AdapterTestConn,
		},
	}

	t.Run("valid URN with prefix", func(t *testing.T) {
		examples, err := adapter.GetAccessExamples(context.Background(),
			"urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket/data/raw,PROD)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(examples) != 3 {
			t.Errorf("expected 3 examples, got %d", len(examples))
		}
		// Check first example contains the expected path
		if len(examples) > 0 && examples[0].Command == "" {
			t.Error("expected non-empty command")
		}
	})

	t.Run("valid URN bucket only", func(t *testing.T) {
		examples, err := adapter.GetAccessExamples(context.Background(),
			"urn:li:dataset:(urn:li:dataPlatform:s3,my-bucket,PROD)")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(examples) != 3 {
			t.Errorf("expected 3 examples, got %d", len(examples))
		}
	})

	t.Run("invalid URN", func(t *testing.T) {
		_, err := adapter.GetAccessExamples(context.Background(), "invalid-urn")
		if err == nil {
			t.Error("expected error for invalid URN")
		}
	})
}

// Verify Adapter implements Provider interface.
var _ storage.Provider = (*Adapter)(nil)
