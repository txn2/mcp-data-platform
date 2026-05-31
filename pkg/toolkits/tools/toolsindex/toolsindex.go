// Package toolsindex is the tools-discovery consumer of the indexjobs
// framework (#440). It embeds every globally-visible MCP tool's
// descriptor (name + description + parameter-schema summary) under
// source_kind "tools" and ranks them by cosine similarity for
// platform_find_tools.
//
// Unlike the api-catalog consumer, the tool corpus is not a DB table:
// tools are registered in-process from compiled-in toolkits plus admin
// visibility config. So the Source (in pkg/platform) enumerates the
// live registry; this package owns only the vector storage (a Sink
// over tool_embeddings) and the query-time ranking. The expected-count
// breadcrumb the reconciler diffs against lives in the framework-owned
// index_sources table (migration 000053).
package toolsindex

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "tools"

// SourceID is the single logical tool-corpus identifier. There is one
// tool registry per deployment, identical across replicas (same binary
// plus the same DB-backed visibility config), so a constant source_id
// is sufficient; vectors keyed on it are shared by every replica.
const SourceID = "platform"

// ScoredTool is one tool name with its cosine similarity to a query,
// returned by the store's similarity ranking. Score is in [-1, 1]
// (1 = identical direction); for the platform's normalized embeddings
// it is effectively [0, 1].
type ScoredTool struct {
	ToolName string
	Score    float64
}
