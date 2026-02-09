package tuning

import "maps"

// HintManager manages tool hints.
type HintManager struct {
	hints map[string]string // tool name -> hint
}

// NewHintManager creates a new hint manager.
func NewHintManager() *HintManager {
	return &HintManager{
		hints: make(map[string]string),
	}
}

// SetHint sets a hint for a tool.
func (m *HintManager) SetHint(toolName, hint string) {
	m.hints[toolName] = hint
}

// GetHint gets a hint for a tool.
func (m *HintManager) GetHint(toolName string) (string, bool) {
	hint, ok := m.hints[toolName]
	return hint, ok
}

// SetHints sets multiple hints at once.
func (m *HintManager) SetHints(hints map[string]string) {
	maps.Copy(m.hints, hints)
}

// All returns all hints.
func (m *HintManager) All() map[string]string {
	result := make(map[string]string)
	maps.Copy(result, m.hints)
	return result
}

// DefaultHints returns default hints for common tools.
func DefaultHints() map[string]string {
	return map[string]string{
		"datahub_search":        "Start here to discover datasets by name, description, or tags",
		"datahub_get_entity":    "Get detailed metadata for a specific dataset or entity",
		"datahub_get_lineage":   "Understand data flow and dependencies",
		"trino_query":           "Execute SQL queries against data",
		"trino_describe_table":  "Get table schema with semantic context",
		"trino_show_tables":     "List available tables in a schema",
		"trino_show_catalogs":   "List available catalogs",
		"s3_list_buckets":       "List available S3 buckets",
		"s3_list_objects":       "List objects in a bucket with optional prefix",
		"s3_get_object_preview": "Preview the first few lines of an object",
		"capture_insight":       "Record domain knowledge for admin review and catalog integration",
	}
}
