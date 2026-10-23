import { useId } from "react";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { EditorSection } from "./sections";
import type { PersonaDraft } from "./types";

type Mode = "add" | "replace";

// One setting with one text: the persona's text is added to the platform's,
// or replaces it. Two fields used to sit side by side, and the replacing one
// silently discarded the other (#2257).
function TextSetting({
  label,
  addLabel,
  replaceLabel,
  addValue,
  replaceValue,
  onChange,
  isReadOnly,
}: {
  label: string;
  addLabel: string;
  replaceLabel: string;
  addValue: string;
  replaceValue: string;
  onChange: (add: string, replace: string) => void;
  isReadOnly: boolean;
}) {
  const ids = useId();
  const mode: Mode = replaceValue ? "replace" : "add";
  const text = mode === "replace" ? replaceValue : addValue;
  const setMode = (m: Mode) => (m === "replace" ? onChange("", text) : onChange(text, ""));
  return (
    <fieldset disabled={isReadOnly} className="space-y-2">
      <legend className="text-sm font-medium">{label}</legend>
      <div className="flex flex-wrap gap-4 text-xs">
        {(
          [
            ["add", addLabel],
            ["replace", replaceLabel],
          ] as const
        ).map(([m, l]) => (
          <label key={m} className="flex items-center gap-1.5">
            <input type="radio" name={`${ids}-mode`} checked={mode === m} onChange={() => setMode(m)} />
            {l}
          </label>
        ))}
      </div>
      <MarkdownEditor
        value={text}
        onChange={(v) => (mode === "replace" ? onChange("", v) : onChange(v, ""))}
        minHeight="140px"
        readOnly={isReadOnly}
      />
    </fieldset>
  );
}

// InstructionsSection is the text platform_info returns to the persona's AI
// assistant. It grants nothing.
export function InstructionsSection({
  draft,
  onUpdate,
  isReadOnly,
}: {
  draft: PersonaDraft;
  onUpdate: (partial: Partial<PersonaDraft>) => void;
  isReadOnly: boolean;
}) {
  return (
    <EditorSection id="instructions" title="Instructions" governs="The text platform_info returns to the persona's AI assistant. Nothing here grants access.">
      <div className="max-w-3xl space-y-6">
        <TextSetting
          label="Tool descriptions"
          addLabel="Add to platform text"
          replaceLabel="Replace platform text"
          addValue={draft.descriptionPrefix}
          replaceValue={draft.descriptionOverride}
          onChange={(add, replace) => onUpdate({ descriptionPrefix: add, descriptionOverride: replace })}
          isReadOnly={isReadOnly}
        />
        <TextSetting
          label="Agent instructions"
          addLabel="Add"
          replaceLabel="Replace"
          addValue={draft.agentInstructionsSuffix}
          replaceValue={draft.agentInstructionsOverride}
          onChange={(add, replace) => onUpdate({ agentInstructionsSuffix: add, agentInstructionsOverride: replace })}
          isReadOnly={isReadOnly}
        />
      </div>
    </EditorSection>
  );
}
