import { CtxField } from "./primitives";
import type { PersonaDraft } from "./types";

// BehaviorTab is the persona editor's "AI Assistant Behavior" tab: the
// description/agent-instruction prefix/suffix/override editors that inject
// persona-specific guidance into what MCP clients see. Extracted from
// PersonaEditor.tsx (#766).
export function BehaviorTab({
  draft,
  onUpdate,
  isReadOnly,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  isReadOnly: boolean;
}) {
  return (
    <div className="flex-1 overflow-y-auto px-6 py-5">
      <p className="mb-5 text-xs text-muted-foreground">
        Inject persona-specific guidance into the platform description and
        agent instructions that MCP clients see. Prefix/suffix variants
        append to the platform defaults; override variants replace them
        entirely.
      </p>
      <fieldset disabled={isReadOnly} className="contents">
        <div className="space-y-5">
          <CtxField
            label="Description Prefix"
            value={draft.descriptionPrefix}
            onChange={(v) => onUpdate({ descriptionPrefix: v })}
            minHeight="160px"
            readOnly={isReadOnly}
          />
          <CtxField
            label="Description Override"
            value={draft.descriptionOverride}
            onChange={(v) => onUpdate({ descriptionOverride: v })}
            minHeight="160px"
            readOnly={isReadOnly}
          />
          <CtxField
            label="Agent Instructions Suffix"
            value={draft.agentInstructionsSuffix}
            onChange={(v) => onUpdate({ agentInstructionsSuffix: v })}
            minHeight="200px"
            readOnly={isReadOnly}
          />
          <CtxField
            label="Agent Instructions Override"
            value={draft.agentInstructionsOverride}
            onChange={(v) => onUpdate({ agentInstructionsOverride: v })}
            minHeight="200px"
            readOnly={isReadOnly}
          />
        </div>
      </fieldset>
    </div>
  );
}
