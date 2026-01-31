// Package queryresults provides an MCP App for interactive query result tables.
package queryresults

import (
	"embed"

	"github.com/txn2/mcp-data-platform/pkg/mcpapps"
)

//go:embed assets/*
var assets embed.FS

// Config holds configuration for the query results app.
type Config struct {
	// ChartCDN is the URL for the Chart.js library.
	// Defaults to jsDelivr CDN.
	ChartCDN string `yaml:"chart_cdn" json:"chartCDN"`

	// DefaultChartType is the default chart type when creating visualizations.
	// Valid values: "bar", "line", "pie".
	DefaultChartType string `yaml:"default_chart_type" json:"defaultChartType"`

	// MaxTableRows is the maximum number of rows to render in the table.
	// This is a performance limit to prevent browser slowdown.
	MaxTableRows int `yaml:"max_table_rows" json:"maxTableRows"`
}

// DefaultConfig returns sensible defaults for the query results app.
func DefaultConfig() Config {
	return Config{
		ChartCDN:         "https://cdn.jsdelivr.net/npm/chart.js",
		DefaultChartType: "bar",
		MaxTableRows:     1000,
	}
}

// App creates an AppDefinition for the query results interactive table.
func App(cfg Config) *mcpapps.AppDefinition {
	// Apply defaults for zero values
	defaults := DefaultConfig()
	if cfg.ChartCDN == "" {
		cfg.ChartCDN = defaults.ChartCDN
	}
	if cfg.DefaultChartType == "" {
		cfg.DefaultChartType = defaults.DefaultChartType
	}
	if cfg.MaxTableRows == 0 {
		cfg.MaxTableRows = defaults.MaxTableRows
	}

	return &mcpapps.AppDefinition{
		Name:        "query-results",
		ResourceURI: "ui://query-results",
		ToolNames:   []string{"trino_query"},
		Assets:      assets,
		AssetsRoot:  "assets",
		EntryPoint:  "index.html",
		Config:      cfg,
	}
}
