package configstore

import (
	"context"
	"errors"
	"testing"
)

const testHistoryLimit = 10

// testConfig is a simple struct for testing (avoids importing platform).
type testConfig struct {
	Server struct {
		Name string `yaml:"name"`
	} `yaml:"server"`
}

func TestFileStore_Load(t *testing.T) {
	cfg := &testConfig{}
	cfg.Server.Name = "test"
	store := NewFileStore(cfg)

	data, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(data) == 0 {
		t.Error("Load() returned empty data")
	}
}

func TestFileStore_Save_ReturnsReadOnly(t *testing.T) {
	store := NewFileStore(&testConfig{})

	err := store.Save(context.Background(), []byte("data"), SaveMeta{})
	if !errors.Is(err, ErrReadOnly) {
		t.Errorf("Save() error = %v, want ErrReadOnly", err)
	}
}

func TestFileStore_History_ReturnsNil(t *testing.T) {
	store := NewFileStore(&testConfig{})

	revisions, err := store.History(context.Background(), testHistoryLimit)
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if revisions != nil {
		t.Errorf("History() = %v, want nil", revisions)
	}
}

func TestFileStore_Mode(t *testing.T) {
	store := NewFileStore(&testConfig{})

	if got := store.Mode(); got != "file" {
		t.Errorf("Mode() = %q, want %q", got, "file")
	}
}
