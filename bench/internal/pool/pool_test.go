package pool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArmConfigsDefineExactlySize is the guard behind Size: every arm config
// must define identities 001..Size, contiguously and with no extras. A pool
// smaller than the flag default is the expensive failure — the runner checks
// its flag against what a run needs, never against the configs, so an attempt
// authenticates as an identity nobody defined and a paid run dies partway in.
// A pool larger than the default is quieter but still wrong: those identities
// are unreachable, so a run that should have refused to start proceeds.
func TestArmConfigsDefineExactlySize(t *testing.T) {
	t.Parallel()
	configs, err := filepath.Glob(filepath.Join("..", "..", "config", "platform.bench.a*.yaml"))
	if err != nil {
		t.Fatalf("glob arm configs: %v", err)
	}
	// A glob that matches nothing would pass every assertion below without
	// reading a single config.
	if len(configs) != 4 {
		t.Fatalf("found %d arm configs (%v), want the four a0-a3 configs", len(configs), configs)
	}
	for _, path := range configs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			body := string(b)
			for seq := 1; seq <= Size; seq++ {
				want := fmt.Sprintf("name: %q", fmt.Sprintf("%s-%03d", NamePrefix, seq))
				if !strings.Contains(body, want) {
					t.Fatalf("%s does not define identity %d (looked for %s); the pool is smaller than pool.Size=%d, so a run at the default -identity-keys authenticates as an identity that does not exist", path, seq, want, Size)
				}
			}
			absent := fmt.Sprintf("name: %q", fmt.Sprintf("%s-%03d", NamePrefix, Size+1))
			if strings.Contains(body, absent) {
				t.Errorf("%s defines identity %d, beyond pool.Size=%d; raise Size so -identity-keys can reach it, or drop the extras", path, Size+1, Size)
			}
		})
	}
}

func TestCredential(t *testing.T) {
	// Rotation off (identityKeys == 0): the base credential is used verbatim.
	if got := Credential("base-key", 5, 0); got != "base-key" {
		t.Errorf("Credential rotation off = %q, want base-key", got)
	}
	// Rotation on: zero-padded three-digit pool key.
	if got := Credential("base-key", 7, Size); got != "base-key-007" {
		t.Errorf("Credential rotation on = %q, want base-key-007", got)
	}
	if got := Credential("base-key", 123, Size); got != "base-key-123" {
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
