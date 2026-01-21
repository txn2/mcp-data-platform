package server

import (
	"testing"
)

func TestNewWithDefaults(t *testing.T) {
	s, toolkit, err := NewWithDefaults()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s == nil {
		t.Error("expected non-nil server")
	}

	if toolkit == nil {
		t.Error("expected non-nil toolkit")
	}
}

func TestVersion(t *testing.T) {
	// Version should be set to "dev" by default
	if Version != "dev" {
		t.Errorf("expected Version 'dev', got %q", Version)
	}
}
