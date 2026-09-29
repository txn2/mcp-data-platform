import { CtxField } from "./primitives";
import type { PersonaDraft } from "./types";

// BehaviorTab is the persona editor's "AI Assistant Behavior" tab: the
// description/agent-instruction prefix/suffix/override editors that inject
// persona-specific guidance into what MCP clients see, and the service-account
// setting that keeps an automated caller's calls out of Calls (#1980).
// Extracted from PersonaEditor.tsx (#766).
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
          {/* A native checkbox: no checkbox primitive is vendored, and this is
              the persona's one binary setting (#1980). */}
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={draft.serviceAccount}
              onChange={(e) => onUpdate({ serviceAccount: e.target.checked })}
              aria-describedby="persona-service-account-help"
            />
            <span>
              <span className="font-medium">Service account</span>
              <span
                id="persona-service-account-help"
                className="block text-xs text-muted-foreground"
              >
                Its calls are audited but not added to Calls.
              </span>
            </span>
          </label>
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
