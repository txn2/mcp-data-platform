package instructions

import "strings"

// ResourcePositioning states what managed resources are for, relative to the
// platform's three other content layers, in the wording every surface uses.
//
// It is the single source of truth for that wording. The agent-facing note
// below embeds it, the portal renders it from ui/src/lib/positioning.ts, and
// docs/concepts/content-model.md and docs/portal/resources.md quote it;
// TestResourcePositioningIsVerbatim
// fails when any of those copies drifts from this constant.
const ResourcePositioning = "Resources are human-uploaded inputs an agent uses as-is: report templates, " +
	"brand files, data dictionaries, sample payloads, and reference documents. Assets are AI-generated " +
	"outputs. Knowledge pages are curated facts to search and synthesize. Memory is per-user recall. " +
	"If it existed before the conversation and the agent should use it verbatim, it is a resource."

// ResourcesNote returns the managed-resources section of the agent
// instructions: the positioning statement above plus the operating rule that
// makes it actionable (consult a template before formatting a deliverable,
// resolve a file the user names by its own vocabulary, treat prompt attachments
// as authoritative).
//
// The caller appends it as a runtime note only when the deployment actually has
// managed resources. Like Build, it names a tool only when that tool is in
// accessibleTools: a caller who can reach `search` is steered through discovery,
// and a caller who cannot is steered to the resources/list protocol method,
// which is persona-filtered but is not a tool and is therefore always reachable.
func ResourcesNote(accessibleTools []string) string {
	has := toolSet(accessibleTools)

	var bullets []string
	if has[toolSearch] {
		bullets = append(bullets,
			"Before you format a deliverable, `search` for an applicable template or reference "+
				"resource and follow it rather than inventing a layout.",
			namedFileBullet(has[toolFetch]))
	} else {
		bullets = append(bullets,
			"Call `resources/list` to see what reference material this deployment carries, and read "+
				"what applies before you format a deliverable rather than inventing a layout.")
	}
	bullets = append(bullets,
		"Material attached to a prompt is authoritative: use it as given rather than paraphrasing it "+
			"or substituting your own.")

	lines := make([]string, 0, len(bullets)+2)
	lines = append(lines, "Uploaded reference material:", ResourcePositioning)
	for _, bullet := range bullets {
		lines = append(lines, "- "+bullet)
	}
	return strings.Join(lines, "\n")
}

// namedFileBullet returns the "the user named a company file" instruction,
// naming `fetch` as the way to read the resolved file in full only when the
// caller can reach it (fetch is registered alongside search but a persona may
// deny it).
func namedFileBullet(hasFetch bool) string {
	const head = "When the user names a company file (\"our template\", \"the checklist\", " +
		"\"the brand header\"), resolve it with `search` "
	const tail = "instead of asking them to paste it."
	if hasFetch {
		return head + "and read it in full with `fetch` (pass the result's `reference`) " + tail
	}
	return head + tail
}
