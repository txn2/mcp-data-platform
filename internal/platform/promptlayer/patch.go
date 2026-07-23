package promptlayer

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/textpatch/patchmcp"
)

// manage_prompt commands that read or edit prompt content through the shared
// textpatch grammar (#1033). They are the same verbs, with the same argument
// names and the same error codes, as the manage_asset content actions.
const (
	cmdPatch      = "patch"
	cmdLocate     = "locate"
	cmdGetContent = "get_content"
	cmdOutline    = "outline"
	cmdStats      = "stats"
	cmdDiff       = "diff"
)

// fieldVersion is the JSON result key for a prompt's served version.
const fieldVersion = "version"

// promptIdentity is the prompt half of every content-verb response. The body
// half comes from pkg/textpatch, so manage_prompt and manage_asset answer the
// same shape for the same question.
func promptIdentity(pr *prompt.Prompt) map[string]any {
	return map[string]any{
		fieldName:    pr.Name,
		fieldVersion: pr.Version,
		fieldStatus:  pr.Status,
	}
}

// contentVerb runs a read-only content verb: resolve the prompt the caller may
// see, build the body fields, and stamp the prompt's identity onto them.
func (h *Handle) contentVerb(
	ctx context.Context,
	input managePromptInput,
	build func(body string) (map[string]any, error),
) (*mcp.CallToolResult, any, error) {
	pr, errResult := h.readablePrompt(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	fields, err := build(pr.Content)
	if err != nil {
		return patchmcp.ErrorResult(err), nil, nil
	}
	maps.Copy(fields, promptIdentity(pr))
	return promptJSONResult(fields)
}

// handlePromptOutline returns the prompt's heading tree.
func (h *Handle) handlePromptOutline(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.OutlineFields(body), nil
	})
}

// handlePromptStats returns the prompt's size, line count, version, and body
// hash, with none of the body.
func (h *Handle) handlePromptStats(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.StatsFields(body), nil
	})
}

// handlePromptGetContent reads the whole prompt body, one section, or a line
// range.
func (h *Handle) handlePromptGetContent(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.ContentFields(body, textpatch.ContentRequest{
			Section:   input.Section,
			LineStart: input.LineStart,
			LineEnd:   input.LineEnd,
		})
	})
}

// handlePromptLocate reports every match of a literal or regex anchor in the
// prompt body, so an agent can pick a unique anchor before patching.
func (h *Handle) handlePromptLocate(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	return h.contentVerb(ctx, input, func(body string) (map[string]any, error) {
		return textpatch.LocateFields(body, textpatch.LocateQuery{
			Find:         input.Find,
			Pattern:      input.Pattern,
			Section:      input.Section,
			ContextBytes: input.ContextBytes,
			Limit:        input.Limit,
		}, textpatch.Options{})
	})
}

// handlePromptPatch applies anchored edits to a prompt's content and lands the
// result through the same review gate as any other content edit: patching an
// approved shared prompt produces a pending draft version and the approved
// snapshot keeps being served until an admin approves it.
func (h *Handle) handlePromptPatch(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	existing, errResult := h.editablePrompt(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if input.BaseVersion > 0 && input.BaseVersion != existing.Version {
		return patchmcp.ErrorResult(textpatch.StaleBaseError(input.BaseVersion, existing.Version)), nil, nil
	}

	res, err := textpatch.Apply(existing.Content, input.Edits, textpatch.Options{})
	if err != nil {
		return patchmcp.ErrorResult(err), nil, nil
	}

	report := textpatch.PatchFields(res)
	if input.DryRun {
		maps.Copy(report, promptIdentity(existing))
		report["dry_run"] = true
		report["message"] = "Dry run: no version was created."
		return promptJSONResult(report)
	}

	before := *existing
	existing.Content = res.Body
	return h.persistPromptEdit(ctx, promptEdit{
		before:   &before,
		after:    existing,
		oldScope: existing.Scope,
		email:    resolveEmail(ctx),
		extra:    report,
	})
}

// handlePromptDiff compares two versions of a prompt. With no versions named it
// compares the newest pending draft against the approved snapshot being served,
// which is the question an admin reviewing a draft actually has.
func (h *Handle) handlePromptDiff(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	pr, errResult := h.readablePrompt(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	versions, ok := h.store.(prompt.VersionStore)
	if !ok {
		return promptErrorResult("prompt versioning is unavailable on this deployment"), nil, nil
	}

	// One listing answers both which versions to compare and what they hold,
	// so the bodies are never fetched a second time.
	history, _ := versions.ListVersions(ctx, pr.ID)
	from, to, err := resolveDiffVersions(history, pr, input)
	if err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}

	oldBody, err := versionContent(ctx, versions, history, pr, from)
	if err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}
	newBody, err := versionContent(ctx, versions, history, pr, to)
	if err != nil {
		return promptErrorResult(err.Error()), nil, nil
	}

	return promptJSONResult(map[string]any{
		fieldName:      pr.Name,
		"from_version": from,
		"to_version":   to,
		textpatch.FieldDiff: textpatch.UnifiedDiffLabeled(
			oldBody, newBody, fmt.Sprintf("v%d", from), fmt.Sprintf("v%d", to), 0),
	})
}

// resolveDiffVersions picks the two versions a diff compares: the caller's
// explicit choice, or the newest pending draft against the served version.
func resolveDiffVersions(history []prompt.Version, pr *prompt.Prompt, input managePromptInput) (from, to int, err error) {
	from, to = input.FromVersion, input.ToVersion
	if to <= 0 {
		to = pendingDraftVersion(history)
	}
	if to <= 0 {
		to = pr.Version
	}
	if from <= 0 {
		from = pr.Version
		if from == to {
			from = to - 1
		}
	}
	if from < 1 || to < 1 {
		return 0, 0, errors.New("this prompt has no earlier version to compare against")
	}
	return from, to, nil
}

// pendingDraftVersion returns the newest draft version awaiting approval, or 0
// when the prompt has none.
func pendingDraftVersion(history []prompt.Version) int {
	best := 0
	for _, v := range history {
		if v.Status == prompt.VersionStatusDraft && v.Version > best {
			best = v.Version
		}
	}
	return best
}

// versionContent returns one version's content, preferring the already-listed
// history and falling back to the live row for the version the prompt currently
// serves (a prompt created before versioning was recorded has no version row).
func versionContent(ctx context.Context, versions prompt.VersionStore, history []prompt.Version, pr *prompt.Prompt, version int) (string, error) {
	for _, v := range history {
		if v.Version == version {
			return v.Content, nil
		}
	}
	v, err := versions.GetVersion(ctx, pr.ID, version)
	if err != nil {
		return "", fmt.Errorf("failed to read version %d", version)
	}
	if v == nil {
		if version == pr.Version {
			return pr.Content, nil
		}
		return "", fmt.Errorf("version %d not found", version)
	}
	return v.Content, nil
}

// readablePrompt resolves the prompt a content command names and checks the
// caller may see it: the same visibility rule the get command applies.
func (h *Handle) readablePrompt(ctx context.Context, input managePromptInput) (*prompt.Prompt, *mcp.CallToolResult) {
	if input.Name == "" {
		return nil, promptErrorResult("name is required")
	}
	pr, err := h.resolveManagedPrompt(ctx, input.Name, resolveEmail(ctx), input.Scope, input.OwnerEmail)
	if err != nil {
		return nil, h.promptErrorDetail(ctx, promptErrGet, err)
	}
	if pr == nil {
		return nil, promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name))
	}
	if !h.isAdminPersona(ctx) && pr.Scope == prompt.ScopePersonal && pr.OwnerEmail != resolveEmail(ctx) {
		return nil, promptErrorResult("you can only view your own personal prompts")
	}
	return pr, nil
}

// editablePrompt resolves the prompt a mutation names and checks the caller may
// change it: it must exist, be visible, not be a read-only config mirror, and
// belong to the caller unless they are an admin. It is the one place those
// three rules are applied, so update and patch cannot drift apart.
func (h *Handle) editablePrompt(ctx context.Context, input managePromptInput) (*prompt.Prompt, *mcp.CallToolResult) {
	pr, errResult := h.readablePrompt(ctx, input)
	if errResult != nil {
		return nil, errResult
	}
	if pr.Source == prompt.SourceSystem {
		return nil, promptErrorResult("this prompt is defined in server configuration or built in and is read-only; edit it in config")
	}
	if msg := authorizePromptMutation(pr, resolveEmail(ctx), "update", h.isAdminPersona(ctx)); msg != "" {
		return nil, promptErrorResult(msg)
	}
	return pr, nil
}
