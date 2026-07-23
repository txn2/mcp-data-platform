// Package patchmcp renders textpatch failures as MCP tool results carrying the
// platform's error contract.
//
// It is the one adapter between the pure-text edit engine and the protocol, so
// a PATCH_AMBIGUOUS from manage_asset and one from manage_prompt are the same
// envelope with the same code and the same corrective hint. pkg/textpatch stays
// free of MCP; every tool that adopts the grammar routes its errors here.
package patchmcp

import (
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// ErrorResult converts an error from pkg/textpatch into a categorized tool
// result. A textpatch.Error keeps its stable PATCH_* code and hint; any other
// error is reported as a generic tool error so a result is never an opaque
// string.
func ErrorResult(err error) *mcp.CallToolResult {
	var pe *textpatch.Error
	if !errors.As(err, &pe) {
		return middleware.BuildErrorResult(middleware.NewToolError(
			middleware.CodeToolError, middleware.ErrCategoryToolError, err.Error(), ""))
	}
	return middleware.BuildErrorResult(middleware.NewToolError(
		pe.Code, category(pe.Code), pe.Error(), pe.Hint))
}

// category maps a patch error code onto the platform error taxonomy. Every
// patch failure is caller-correctable: the anchor, the occurrence, the base
// version, or the target is wrong, and the hint says which.
func category(code string) string {
	switch code {
	case textpatch.CodeSectionNotFound, textpatch.CodeNoMatch:
		return middleware.ErrCategoryNotFound
	default:
		return middleware.ErrCategoryClientInput
	}
}
