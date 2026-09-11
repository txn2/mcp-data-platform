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

// ReadOnlyAnnotations is what a tool that only reads advertises: it does not
// modify its environment, and calling it again with the same arguments changes
// nothing. A client that honors readOnlyHint auto-approves such a tool instead
// of asking the user to confirm a write (#1692).
//
// It returns a fresh value on every call because mcp.ToolAnnotations is
// mutable and each registration hands the SDK its own.
func ReadOnlyAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
}

// WriteAnnotations is what a tool that can modify its environment advertises.
// destructive says whether any action the tool admits removes or overwrites
// state that is already there; a tool exposing both reads and writes is a
// write, because the annotation describes the most it can do.
//
// The hint is stated rather than left out: the MCP default for an absent
// destructiveHint is true, so a purely additive write that omits it is read as
// one that may destroy (#1692).
func WriteAnnotations(destructive bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive}
}
