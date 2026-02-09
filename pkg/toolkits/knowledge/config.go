package knowledge

// ParseConfig extracts an InsightStore from a configuration map.
// The store must be provided programmatically (not from YAML).
func ParseConfig(cfg map[string]any) InsightStore {
	if store, ok := cfg["store"].(InsightStore); ok {
		return store
	}

	return nil
}
