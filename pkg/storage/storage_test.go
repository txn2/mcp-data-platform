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
		if result != nil {
			t.Errorf("expected nil, got %v", result)
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
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("ListObjects", func(t *testing.T) {
		result, err := p.ListObjects(ctx, DatasetIdentifier{Bucket: "test"}, 10)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("Close", func(t *testing.T) {
		err := p.Close()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestDatasetAvailability(t *testing.T) {
	avail := DatasetAvailability{
		Available:   true,
		Bucket:      "test-bucket",
		Prefix:      "data/",
		Connection:  "main",
		ObjectCount: 100,
		TotalSize:   1024000,
	}

	if !avail.Available {
		t.Error("expected Available to be true")
	}
	if avail.Bucket != "test-bucket" {
		t.Errorf("expected bucket 'test-bucket', got %q", avail.Bucket)
	}
	if avail.ObjectCount != 100 {
		t.Errorf("expected ObjectCount 100, got %d", avail.ObjectCount)
	}
}

func TestObjectInfo(t *testing.T) {
	obj := ObjectInfo{
		Key:         "data/file.parquet",
		Bucket:      "test-bucket",
		Size:        1024,
		ContentType: "application/octet-stream",
		Metadata:    map[string]string{"version": "1.0"},
	}

	if obj.Key != "data/file.parquet" {
		t.Errorf("expected key 'data/file.parquet', got %q", obj.Key)
	}
	if obj.Size != 1024 {
		t.Errorf("expected size 1024, got %d", obj.Size)
	}
	if obj.Metadata["version"] != "1.0" {
		t.Errorf("expected metadata version '1.0', got %q", obj.Metadata["version"])
	}
}

func TestAccessExample(t *testing.T) {
	ex := AccessExample{
		Description: "List objects",
		Command:     "aws s3 ls s3://bucket/",
		SDK:         "aws-sdk-go",
	}

	if ex.Description != "List objects" {
		t.Errorf("expected description 'List objects', got %q", ex.Description)
	}
	if ex.Command != "aws s3 ls s3://bucket/" {
		t.Errorf("expected command 'aws s3 ls s3://bucket/', got %q", ex.Command)
	}
}

// Verify NoopProvider implements Provider interface.
var _ Provider = (*NoopProvider)(nil)
