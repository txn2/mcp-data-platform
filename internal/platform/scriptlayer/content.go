package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/textpatch/patchmcp"
)

// scriptSyntax is the textpatch syntax scripts are navigated with. Starlark is
// indentation-structured source, not markdown and not an element tree, so
// neither region grammar applies: SyntaxNone gives anchored and line-addressed
// editing, which is what an author fixing one function actually wants, and
// refuses a section or selector with a message naming what the document
// supports instead of silently matching a `#` comment as a heading.
const scriptSyntax = textpatch.SyntaxNone

// handleOutline returns the script's structure.
func (h *Handle) handleOutline(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.OutlineFields(body, scriptSyntax), nil
	})
}

// handleStats returns the script's size, line count, and body hash, with none
// of the source.
func (h *Handle) handleStats(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.StatsFields(body), nil
	})
}

// handleGetContent reads the whole source, one section, or a line range.
func (h *Handle) handleGetContent(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.ContentFields(body, textpatch.ContentRequest{
			Syntax:     scriptSyntax,
			Section:    input.Section,
			Selector:   input.Selector,
			Occurrence: input.Occurrence,
			LineStart:  input.LineStart,
			LineEnd:    input.LineEnd,
		})
	})
}

// handleLocate reports every match of a literal or regex anchor, so an author
// can pick a unique anchor before patching.
func (h *Handle) handleLocate(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.LocateFields(body, textpatch.LocateQuery{
			Find:         input.Find,
			Pattern:      input.Pattern,
			Section:      input.Section,
			Selector:     input.Selector,
			Occurrence:   input.Occurrence,
			ContextBytes: input.ContextBytes,
			Limit:        input.Limit,
		}, textpatch.Options{Syntax: scriptSyntax})
	})
}

// handlePatch applies anchored edits to a script's source and lands the result
// through the same gate as any other source edit. The patched source is
// validated before it is saved, on the same reasoning as create: a patch that
// leaves the script unparseable is a mistake caught now rather than at the next
// run.
func (h *Handle) handlePatch(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if input.BaseVersion > 0 && input.BaseVersion != existing.Version {
		return patchmcp.ErrorResult(textpatch.StaleBaseError(input.BaseVersion, existing.Version)), nil, nil
	}
	res, err := textpatch.Apply(existing.Source, input.Edits, textpatch.Options{Syntax: scriptSyntax})
	if err != nil {
		return patchmcp.ErrorResult(err), nil, nil
	}

	report := textpatch.PatchFields(res)
	if input.DryRun {
		maps.Copy(report, identity(existing))
		report["dry_run"] = true
		report["message"] = "Dry run: no version was created."
		return jsonResult(report)
	}
	if validation := scriptrun.Validate(res.Body); !validation.OK {
		return jsonResult(refusedReport("the patched source does not parse, so the edit was not saved", validation))
	}

	before := *existing
	existing.Source = res.Body
	// The record check that create and update both run. Without it a patch is
	// the one way past it: an edit that deletes the whole body leaves an empty
	// source that parses fine, and repeated inserts walk a script past the size
	// cap that bounds the parser, the history, and the review surface.
	if err := existing.Validate(); err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return h.persist(ctx, &before, existing, report)
}

// handleDiff compares two versions of a script. With no versions named it
// compares the newest pending draft against the snapshot the live row carries,
// which is the question a reviewer actually has.
func (h *Handle) handleDiff(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	versions, ok := h.store.(script.VersionStore)
	if !ok {
		return errorResult("script versioning is unavailable on this deployment"), nil, nil
	}
	history, err := versions.ListVersions(ctx, sc.ID)
	if err != nil {
		// Swallowing this would report "version N not found" for a script whose
		// history is intact and whose store merely blinked.
		slog.Error("failed to list script versions", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to read the version history"), nil, nil
	}
	from, to, err := resolveDiffVersions(history, sc, input)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	oldBody, err := versionSource(ctx, versions, history, sc, from)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	newBody, err := versionSource(ctx, versions, history, sc, to)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	return jsonResult(map[string]any{
		fieldName: sc.Name, "from_version": from, "to_version": to,
		textpatch.FieldDiff: textpatch.UnifiedDiffLabeled(
			oldBody, newBody, fmt.Sprintf("v%d", from), fmt.Sprintf("v%d", to), 0),
	})
}

// resolveDiffVersions picks the two versions a diff compares.
func resolveDiffVersions(history []script.Version, sc *script.Script, input manageScriptInput) (from, to int, err error) {
	from, to = input.FromVersion, input.ToVersion
	if to <= 0 {
		to = pendingDraftVersion(history)
	}
	if to <= 0 {
		to = sc.Version
	}
	if from <= 0 {
		from = sc.Version
		if from == to {
			from = to - 1
		}
	}
	if from < 1 || to < 1 {
		return 0, 0, errors.New("this script has no earlier version to compare against")
	}
	return from, to, nil
}

// pendingDraftVersion returns the newest draft version awaiting approval, or 0
// when the script has none.
func pendingDraftVersion(history []script.Version) int {
	best := 0
	for _, v := range history {
		if v.Status == script.VersionStatusDraft && v.Version > best {
			best = v.Version
		}
	}
	return best
}

// versionSource returns one version's source, preferring the already-listed
// history and falling back to the live row for the version it carries.
func versionSource(ctx context.Context, versions script.VersionStore, history []script.Version, sc *script.Script, version int) (string, error) {
	for _, v := range history {
		if v.Version == version {
			return v.Source, nil
		}
	}
	v, err := versions.GetVersion(ctx, sc.ID, version)
	if err != nil {
		return "", fmt.Errorf("failed to read version %d", version)
	}
	if v == nil {
		if version == sc.Version {
			return sc.Source, nil
		}
		return "", fmt.Errorf("version %d not found", version)
	}
	return v.Source, nil
}
