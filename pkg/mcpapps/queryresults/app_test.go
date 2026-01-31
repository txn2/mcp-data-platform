package queryresults

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ChartCDN == "" {
		t.Error("ChartCDN should have a default value")
	}

	if cfg.DefaultChartType == "" {
		t.Error("DefaultChartType should have a default value")
	}

	if cfg.MaxTableRows == 0 {
		t.Error("MaxTableRows should have a default value")
	}
}

func TestApp(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		app := App(Config{})

		if app.Name != "query-results" {
			t.Errorf("Name = %q, want %q", app.Name, "query-results")
		}

		if app.ResourceURI != "ui://query-results" {
			t.Errorf("ResourceURI = %q, want %q", app.ResourceURI, "ui://query-results")
		}

		if len(app.ToolNames) != 1 || app.ToolNames[0] != "trino_query" {
			t.Errorf("ToolNames = %v, want [trino_query]", app.ToolNames)
		}

		if app.EntryPoint != "index.html" {
			t.Errorf("EntryPoint = %q, want %q", app.EntryPoint, "index.html")
		}

		// Verify defaults are applied to config
		cfg, ok := app.Config.(Config)
		if !ok {
			t.Fatalf("Config is not Config type: %T", app.Config)
		}

		defaults := DefaultConfig()
		if cfg.ChartCDN != defaults.ChartCDN {
			t.Errorf("Config.ChartCDN = %q, want %q", cfg.ChartCDN, defaults.ChartCDN)
		}
		if cfg.DefaultChartType != defaults.DefaultChartType {
			t.Errorf("Config.DefaultChartType = %q, want %q", cfg.DefaultChartType, defaults.DefaultChartType)
		}
		if cfg.MaxTableRows != defaults.MaxTableRows {
			t.Errorf("Config.MaxTableRows = %d, want %d", cfg.MaxTableRows, defaults.MaxTableRows)
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		customCfg := Config{
			ChartCDN:         "https://custom.cdn/chart.js",
			DefaultChartType: "pie",
			MaxTableRows:     500,
		}

		app := App(customCfg)

		cfg, ok := app.Config.(Config)
		if !ok {
			t.Fatalf("Config is not Config type: %T", app.Config)
		}

		if cfg.ChartCDN != customCfg.ChartCDN {
			t.Errorf("Config.ChartCDN = %q, want %q", cfg.ChartCDN, customCfg.ChartCDN)
		}
		if cfg.DefaultChartType != customCfg.DefaultChartType {
			t.Errorf("Config.DefaultChartType = %q, want %q", cfg.DefaultChartType, customCfg.DefaultChartType)
		}
		if cfg.MaxTableRows != customCfg.MaxTableRows {
			t.Errorf("Config.MaxTableRows = %d, want %d", cfg.MaxTableRows, customCfg.MaxTableRows)
		}
	})

	t.Run("validates successfully", func(t *testing.T) {
		app := App(Config{})

		if err := app.Validate(); err != nil {
			t.Errorf("Validate() returned error: %v", err)
		}
	})

	t.Run("assets contain index.html", func(t *testing.T) {
		app := App(Config{})

		// Try to read the entry point
		content, err := app.Assets.ReadFile("assets/index.html")
		if err != nil {
			t.Fatalf("Failed to read index.html: %v", err)
		}

		if len(content) == 0 {
			t.Error("index.html is empty")
		}
	})
}
