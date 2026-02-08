package storage

import (
	"context"
	"testing"
)

const (
	storageTestBucket        = "my-bucket"
	storageTestListLimit     = 10
	storageTestUnexpectedErr = "unexpected error: %v"
)

func TestDatasetIdentifierString(t *testing.T) {
	tests := []struct {
		name     string
		dataset  DatasetIdentifier
		expected string
	}{
		{
			name:     "bucket only",
			dataset:  DatasetIdentifier{Bucket: storageTestBucket},
			expected: storageTestBucket,
		},
		{
			name:     "bucket with prefix",
			dataset:  DatasetIdentifier{Bucket: storageTestBucket, Prefix: "data/raw"},
			expected: "my-bucket/data/raw",
		},
		{
			name:     "bucket with connection",
			dataset:  DatasetIdentifier{Bucket: storageTestBucket, Connection: "prod-s3"},
			expected: storageTestBucket,
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

func TestNoopProvider_Name(t *testing.T) {
	p := NewNoopProvider()
	if p.Name() != "noop" {
		t.Errorf("expected 'noop', got %q", p.Name())
	}
}

func TestNoopProvider_ResolveDataset(t *testing.T) {
	ctx := context.Background()
	p := NewNoopProvider()
	result, err := p.ResolveDataset(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
	if err != nil {
		t.Errorf(storageTestUnexpectedErr, err)
	}
	if result == nil {
		t.Error("expected non-nil empty identifier")
	}
}

func TestNoopProvider_GetDatasetAvailability(t *testing.T) {
	ctx := context.Background()
	p := NewNoopProvider()
	result, err := p.GetDatasetAvailability(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
	if err != nil {
		t.Errorf(storageTestUnexpectedErr, err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Available {
		t.Error("expected Available to be false")
	}
}

func TestNoopProvider_GetAccessExamples(t *testing.T) {
	ctx := context.Background()
	p := NewNoopProvider()
	result, err := p.GetAccessExamples(ctx, "urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)")
	if err != nil {
		t.Errorf(storageTestUnexpectedErr, err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestNoopProvider_ListObjects(t *testing.T) {
	ctx := context.Background()
	p := NewNoopProvider()
	result, err := p.ListObjects(ctx, DatasetIdentifier{Bucket: "test"}, storageTestListLimit)
	if err != nil {
		t.Errorf(storageTestUnexpectedErr, err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice, got %v", result)
	}
}

func TestNoopProvider_Close(t *testing.T) {
	p := NewNoopProvider()
	err := p.Close()
	if err != nil {
		t.Errorf(storageTestUnexpectedErr, err)
	}
}

// Verify NoopProvider implements Provider interface.
var _ Provider = (*NoopProvider)(nil)
