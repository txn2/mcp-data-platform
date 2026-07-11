package toolkit

import "github.com/modelcontextprotocol/go-sdk/mcp"

// AnnotationConfig is the YAML-configurable set of MCP tool annotation hints
// shared by every toolkit. Each field is a pointer so an unset hint (inherit
// the tool's built-in default) is distinct from an explicit false.
type AnnotationConfig struct {
	ReadOnlyHint    *bool `yaml:"read_only_hint"`
	DestructiveHint *bool `yaml:"destructive_hint"`
	IdempotentHint  *bool `yaml:"idempotent_hint"`
	OpenWorldHint   *bool `yaml:"open_world_hint"`
}

// AnnotationsToMCP converts an AnnotationConfig into MCP tool annotations,
// applying only the hints the operator explicitly set.
func AnnotationsToMCP(cfg AnnotationConfig) *mcp.ToolAnnotations {
	ann := &mcp.ToolAnnotations{}
	if cfg.ReadOnlyHint != nil {
		ann.ReadOnlyHint = *cfg.ReadOnlyHint
	}
	if cfg.DestructiveHint != nil {
		ann.DestructiveHint = cfg.DestructiveHint
	}
	if cfg.IdempotentHint != nil {
		ann.IdempotentHint = *cfg.IdempotentHint
	}
	if cfg.OpenWorldHint != nil {
		ann.OpenWorldHint = cfg.OpenWorldHint
	}
	return ann
}
