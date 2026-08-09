package platform

import (
	"testing"
)

// The checkable-insight marker (#1220) is default-on, and each delivery surface
// builds its own resolver from the query provider it already holds. What
// Platform owns is therefore the toggle and its delivery to those surfaces.
// This asserts the default and the enrichment-middleware side of that delivery;
// the search-federation side reads the same accessor into searchfed.Config,
// whose own tests cover what that field does.
func TestVerifiableInsightsTogglePropagates(t *testing.T) {
	tests := []struct {
		name   string
		toggle *bool
		want   bool
	}{
		{name: "unset defaults on", want: true},
		{name: "explicitly on", toggle: new(true), want: true},
		{name: "explicitly off", toggle: new(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Server: ServerConfig{Name: testServerName}}
			cfg.Knowledge.VerifiableInsights = tt.toggle

			p, err := New(WithConfig(cfg))
			if err != nil {
				t.Fatalf(testNewErrFmt, err)
			}

			if got := p.config.Knowledge.IsVerifiableInsightsEnabled(); got != tt.want {
				t.Errorf("IsVerifiableInsightsEnabled() = %v, want %v", got, tt.want)
			}
			if got := p.buildEnrichmentConfig().VerifiableInsights; got != tt.want {
				t.Errorf("enrichment VerifiableInsights = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsVerifiableInsightsEnabled(t *testing.T) {
	var cfg KnowledgeConfig
	if !cfg.IsVerifiableInsightsEnabled() {
		t.Error("an unset toggle should leave the marker enabled")
	}
	cfg.VerifiableInsights = new(false)
	if cfg.IsVerifiableInsightsEnabled() {
		t.Error("an explicit false should disable the marker")
	}
	cfg.VerifiableInsights = new(true)
	if !cfg.IsVerifiableInsightsEnabled() {
		t.Error("an explicit true should enable the marker")
	}
}
