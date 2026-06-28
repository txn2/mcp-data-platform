package knowledgepage

// PageGuardsConfig is the YAML configuration for the knowledge-page write guards
// (#705): the create-time duplicate gate and the oversized-page split suggestion,
// shared by the MCP apply path and the portal REST create path. Zero values select
// documented defaults; the gate is turned off explicitly with DedupDisabled so a
// deployment that wants no gate is unambiguous (not confused with "left at default").
type PageGuardsConfig struct {
	// DedupThreshold is the cosine similarity [0,1] at or above which creating a page
	// is blocked as a near-duplicate. Defaults to DefaultDedupThreshold. The gate only
	// acts when a real embedding provider is configured (cosine is undefined without).
	DedupThreshold float64 `yaml:"dedup_threshold"`
	// DedupDisabled turns the duplicate gate off entirely.
	DedupDisabled bool `yaml:"dedup_disabled"`
	// OversizeBytes is the body byte size at or above which the (non-blocking) split
	// suggestion fires. Defaults to DefaultOversizeBytes; negative disables this arm.
	OversizeBytes int `yaml:"oversize_bytes"`
	// OversizeSections is the markdown-heading count at or above which the split
	// suggestion fires. Defaults to DefaultOversizeSections; negative disables it.
	OversizeSections int `yaml:"oversize_sections"`
}

// PageGuards are the resolved write-guard thresholds the apply path and portal
// consume. A zero DedupThreshold disables the gate; a zero Oversize* threshold
// disables that arm of the split suggestion.
type PageGuards struct {
	DedupThreshold   float64
	OversizeBytes    int
	OversizeSections int
}

// Resolve applies the documented defaults: DedupDisabled (or a non-positive
// threshold) yields 0 (gate off); an unset threshold yields DefaultDedupThreshold.
// A negative oversize threshold disables that arm (0); an unset one yields the
// default.
func (c PageGuardsConfig) Resolve() PageGuards {
	return PageGuards{
		DedupThreshold:   resolveDedup(c.DedupThreshold, c.DedupDisabled),
		OversizeBytes:    resolveOversize(c.OversizeBytes, DefaultOversizeBytes),
		OversizeSections: resolveOversize(c.OversizeSections, DefaultOversizeSections),
	}
}

func resolveDedup(threshold float64, disabled bool) float64 {
	switch {
	case disabled:
		return 0
	case threshold <= 0:
		return DefaultDedupThreshold
	default:
		return threshold
	}
}

func resolveOversize(value, def int) int {
	switch {
	case value < 0:
		return 0
	case value == 0:
		return def
	default:
		return value
	}
}
