package observability

import (
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantEna  bool
		wantAddr string
	}{
		{
			name:     "defaults when env unset",
			env:      map[string]string{},
			wantEna:  false,
			wantAddr: DefaultListenAddr,
		},
		{
			name:     "enabled true",
			env:      map[string]string{envEnabled: "true"},
			wantEna:  true,
			wantAddr: DefaultListenAddr,
		},
		{
			name:     "enabled 1",
			env:      map[string]string{envEnabled: "1"},
			wantEna:  true,
			wantAddr: DefaultListenAddr,
		},
		{
			name:     "custom addr",
			env:      map[string]string{envEnabled: "true", envListenAddr: "127.0.0.1:9999"},
			wantEna:  true,
			wantAddr: "127.0.0.1:9999",
		},
		{
			name:     "unparsable enabled falls back to default",
			env:      map[string]string{envEnabled: "yes-please"},
			wantEna:  false,
			wantAddr: DefaultListenAddr,
		},
		{
			name:     "empty enabled falls back to default",
			env:      map[string]string{envEnabled: "  "},
			wantEna:  false,
			wantAddr: DefaultListenAddr,
		},
		{
			name:     "whitespace addr trims",
			env:      map[string]string{envEnabled: "true", envListenAddr: "  :8765  "},
			wantEna:  true,
			wantAddr: ":8765",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envEnabled, "")
			t.Setenv(envListenAddr, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			got := ConfigFromEnv()
			if got.Enabled != tc.wantEna {
				t.Errorf("Enabled = %v, want %v", got.Enabled, tc.wantEna)
			}
			if got.ListenAddr != tc.wantAddr {
				t.Errorf("ListenAddr = %q, want %q", got.ListenAddr, tc.wantAddr)
			}
		})
	}
}

func TestParseBoolEnvDefault(t *testing.T) {
	t.Setenv("TEST_OBSV_BOOL", "")
	if got := parseBoolEnv("TEST_OBSV_BOOL", true); !got {
		t.Errorf("empty env should fall through to default; got %v want true", got)
	}
}

func TestStringEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_OBSV_STR", "")
	if got := stringEnvOrDefault("TEST_OBSV_STR", "fallback"); got != "fallback" {
		t.Errorf("empty env should yield default; got %q", got)
	}
	t.Setenv("TEST_OBSV_STR", "explicit")
	if got := stringEnvOrDefault("TEST_OBSV_STR", "fallback"); got != "explicit" {
		t.Errorf("set env should override; got %q", got)
	}
}
