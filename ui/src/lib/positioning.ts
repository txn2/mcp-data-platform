/**
 * The canonical statement of what managed resources are for, relative to the
 * platform's three other content layers.
 *
 * Kept byte-identical to instructions.ResourcePositioning
 * (pkg/platform/instructions/resources.go), which is the source of truth for
 * this wording; TestResourcePositioningIsVerbatim fails when they drift. Every
 * portal surface that states the split renders this constant rather than
 * restating it, so the agent and the person uploading the file are told the
 * same thing.
 */
export const RESOURCE_POSITIONING =
  "Resources are human-uploaded inputs an agent uses as-is: report templates, brand files, data dictionaries, sample payloads, and reference documents. Assets are AI-generated outputs. Knowledge pages are curated facts to search and synthesize. Memory is per-user recall. If it existed before the conversation and the agent should use it verbatim, it is a resource.";
