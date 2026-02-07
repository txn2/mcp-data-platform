package storage

import (
	"context"
	"testing"
)

func TestDatasetIdentifierString(t *testing.T) {
	tests := []struct {
		name     string
		dataset  DatasetIdentifier
		expected string
	}{
		{
			name:     "bucket only",
			dataset:  DatasetIdentifier{Bucket: "my-bucket"},
			expected: "my-bucket",
		},
		{
			name:     "bucket with prefix",
			dataset:  DatasetIdentifier{Bucket: "my-bucket", Prefix: "data/raw"},
			expected: "my-bucket/data/raw",
		},
		{
			name:     "bucket with connection",
			dataset:  DatasetIdentifier{Bucket: "my-bucket", Connection: "prod-s3"},
			expected: "my-bucket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.dataset.String()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestNoopProvider(t *testing.T) {
	ctx := context.Background()
	p := NewNoopProvider()

	t.Run("Name", func(t *testing.T) {
		if p.Name() != "noop" {
			t.Errorf("expected 'noop', got %q", p.Name())
		}
	})

	t.Run("ResolveDataset", func(t *testing.T) {
		result, err := p.ResolveDataset(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result == nil {
			t.Error("expected non-nil empty identifier")
		}
	})

	t.Run("GetDatasetAvailability", func(t *testing.T) {
		result, err := p.GetDatasetAvailability(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Available {
			t.Error("expected Available to be false")
		}
	})

	t.Run("GetAccessExamples", func(t *testing.T) {
		result, err := p.GetAccessExamples(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("ListObjects", func(t *testing.T) {
		result, err := p.ListObjects(ctx, DatasetIdentifier{Bucket: "test"}, 10)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("expected empty slice, got %v", result)
		}
	})

	t.Run("Close", func(t *testing.T) {
		err := p.Close()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// Verify NoopProvider implements Provider interface.
var _ Provider = (*NoopProvider)(nil)
