package pool

import "testing"

func TestCredential(t *testing.T) {
	// Rotation off (identityKeys == 0): the base credential is used verbatim.
	if got := Credential("base-key", 5, 0); got != "base-key" {
		t.Errorf("Credential rotation off = %q, want base-key", got)
	}
	// Rotation on: zero-padded three-digit pool key.
	if got := Credential("base-key", 7, 264); got != "base-key-007" {
		t.Errorf("Credential rotation on = %q, want base-key-007", got)
	}
	if got := Credential("base-key", 123, 264); got != "base-key-123" {
		t.Errorf("Credential = %q, want base-key-123", got)
	}
}

func TestEmail(t *testing.T) {
	if got := Email(1); got != "bench-agent-001@apikey.local" {
		t.Errorf("Email = %q, want bench-agent-001@apikey.local", got)
	}
	if got := Email(42); got != "bench-agent-042@apikey.local" {
		t.Errorf("Email = %q, want bench-agent-042@apikey.local", got)
	}
}
