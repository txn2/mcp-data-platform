package maps

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// SettingsInput is a settings write.
type SettingsInput struct {
	Enabled      bool   `json:"enabled"`
	S3Connection string `json:"s3_connection"`
	Bucket       string `json:"bucket"`
	MaxZoom      int    `json:"max_zoom" example:"14"`
	SourceURL    string `json:"source_url"`
}

// RegionInput adds a region: a preset by id, or a named box.
type RegionInput struct {
	Preset string          `json:"preset,omitempty" example:"united-states"`
	ID     string          `json:"id,omitempty" example:"bay-area"`
	Name   string          `json:"name,omitempty" example:"Bay Area"`
	Bounds *pmtiles.Bounds `json:"bounds,omitempty"`
}

// EstimateInput asks what a region would cost before it is saved.
type EstimateInput struct {
	Preset string          `json:"preset,omitempty" example:"united-states"`
	Bounds *pmtiles.Bounds `json:"bounds,omitempty"`
}

// InputError is a request the caller can correct; its message says how.
type InputError struct{ msg string }

func (e *InputError) Error() string { return e.msg }

func inputErr(format string, args ...any) error {
	return &InputError{msg: fmt.Sprintf(format, args...)}
}

// resolveRegion turns a preset or a box into the region it names.
func resolveRegion(preset string, bounds *pmtiles.Bounds) (Preset, error) {
	if preset != "" {
		p, ok := PresetByID(preset)
		if !ok {
			return Preset{}, inputErr("unknown preset %q", preset)
		}
		return p, nil
	}
	if bounds == nil {
		return Preset{}, inputErr("name a preset or give bounds")
	}
	if err := bounds.Validate(); err != nil {
		return Preset{}, inputErr("bounds: %s", err.Error())
	}
	return Preset{Bounds: *bounds}, nil
}
