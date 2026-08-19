import { useState } from "react";
import { usePortalScriptVersions, useSaveScriptMetadata } from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptMetadataInput } from "@/api/portal/hooks/scripts";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { SectionCard } from "@/components/patterns/SectionCard";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScriptFacetBadges } from "./ScriptFacetBadges";

// ScriptDocumentation is what a script says about itself, read and written on
// the same surface (#1369).
//
// A managed script is complex logic that outlives the conversation that
// produced it, so its description is a DOCUMENT: markdown, rendered here the
// way an asset's description and a knowledge page are rendered elsewhere in the
// portal. It used to be the page header's subtitle, which is a one-line slot —
// and a one-line slot is what an author writes to, so a one-line caption is
// what every script got.
//
// The owner edits all four fields here, which is the point of putting them
// here: an author documenting their own automation should not have to ask an
// agent to do it on a page they are already looking at. It costs them nothing
// in governance either. The review gate keys on the source and the parameter
// contract alone, so this saves immediately and the approved version keeps
// running untouched — while still being captured as a version, because what a
// script claimed to do is part of explaining what one of its runs did.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** owned gates editing; a reader who does not own the script only reads. */
  owned: boolean;
}

export function ScriptDocumentation({ scriptId, contract, owned }: Props) {
  const [editing, setEditing] = useState(false);

  return (
    <SectionCard
      title="About"
      action={
        owned && !editing ? (
          <Button type="button" variant="outline" size="xs" onClick={() => setEditing(true)}>
            Edit
          </Button>
        ) : null
      }
    >
      {editing ? (
        <DocumentationForm
          scriptId={scriptId}
          contract={contract}
          owned={owned}
          onDone={() => setEditing(false)}
        />
      ) : (
        <DocumentationView contract={contract} owned={owned} />
      )}
    </SectionCard>
  );
}

// DocumentationView is the document as a reader has it: how it is filed, then
// what it says. An undocumented script says so rather than rendering an empty
// box, and it says so differently to the person who can fix it.
function DocumentationView({ contract, owned }: { contract: ScriptContract; owned: boolean }) {
  const facets = <ScriptFacetBadges category={contract.category} tags={contract.tags} />;
  if (!contract.description) {
    return (
      <div className="space-y-3">
        {facets}
        <p className="text-sm text-muted-foreground">
          {owned
            ? "This script has no description. Write what it produces, what its parameters mean, and what it assumes, so somebody reading it in six months does not have to read the code."
            : "This script has no description."}
        </p>
      </div>
    );
  }
  return (
    <div className="space-y-3">
      {facets}
      <MarkdownRenderer content={contract.description} bare />
    </div>
  );
}

// DocumentationForm edits the four fields together, because they are one
// decision: what this script is called, what it is for, and how it is filed.
function DocumentationForm({
  scriptId,
  contract,
  owned,
  onDone,
}: {
  scriptId: string;
  contract: ScriptContract;
  owned: boolean;
  onDone: () => void;
}) {
  const save = useSaveScriptMetadata(scriptId);
  const pendingDraft = usePendingDraft(scriptId, owned);
  const [draft, setDraft] = useState<Draft>(() => draftOf(contract));
  const [notice, setNotice] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  const submit = () => {
    setNotice(null);
    setFailure(null);
    save.mutate(inputOf(draft), {
      onSuccess: (res) => {
        // The advisory is the one reason to stay on the form after a save: it
        // is a suggestion about the text still on screen. Without one there is
        // nothing left to do here, so the form closes.
        if (res.description_notice) {
          setNotice(res.description_notice);
          return;
        }
        onDone();
      },
      onError: (e: unknown) =>
        setFailure(e instanceof Error ? e.message : "This could not be saved"),
    });
  };

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Display name" htmlFor="script-display-name">
          <Input
            id="script-display-name"
            value={draft.displayName}
            placeholder={contract.name}
            onChange={(e) => setDraft({ ...draft, displayName: e.target.value })}
          />
        </Field>
        <Field
          label="Category"
          htmlFor="script-category"
          hint="Lowercase letters, digits and hyphens."
        >
          <Input
            id="script-category"
            value={draft.category}
            placeholder="reporting"
            onChange={(e) => setDraft({ ...draft, category: e.target.value })}
          />
        </Field>
      </div>

      <Field label="Tags" htmlFor="script-tags" hint="Separated by commas.">
        <Input
          id="script-tags"
          value={draft.tags}
          placeholder="sales, weekly"
          onChange={(e) => setDraft({ ...draft, tags: e.target.value })}
        />
      </Field>

      <Field label="Description" hint="Markdown. Write it for somebody who has not seen the code.">
        <MarkdownEditor
          value={draft.description}
          label="Script description"
          minHeight="12rem"
          onChange={(value) => setDraft({ ...draft, description: value })}
        />
      </Field>

      {pendingDraft && (
        <Alert>
          <AlertDescription>
            Version {pendingDraft} of this script is waiting for a reviewer. Approving it
            applies everything that version captured, documentation included, so what you
            write here is replaced when that happens. Write it again after the approval.
          </AlertDescription>
        </Alert>
      )}
      {failure && (
        <Alert variant="destructive">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {notice && (
        <Alert>
          <AlertDescription>Saved. {notice}</AlertDescription>
        </Alert>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone} disabled={save.isPending}>
          {notice ? "Done" : "Cancel"}
        </Button>
        <Button type="button" size="sm" onClick={submit} disabled={save.isPending}>
          {save.isPending ? "Saving..." : "Save"}
        </Button>
      </div>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor?: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

// usePendingDraft is the number of the version waiting for a reviewer, or null
// when none is. It exists because approving a version applies that version's
// whole snapshot to the live row, documentation included: an owner documenting a
// script while one of its edits is in the queue would otherwise watch their
// writing disappear at the approval with nothing having said it would.
//
// It reads the version history the page below already requests, on the same
// query key, so it costs no additional request.
function usePendingDraft(scriptId: string, owned: boolean): number | null {
  const { data } = usePortalScriptVersions(scriptId, owned);
  const draft = (data?.data ?? []).find((v) => v.status === "draft");
  return draft ? draft.version : null;
}

// Draft is the form's own state. Tags are one comma-separated string while
// being typed, because a person mid-word between two commas is not yet a list
// and re-splitting on every keystroke would fight them for the cursor.
interface Draft {
  displayName: string;
  description: string;
  category: string;
  tags: string;
}

function draftOf(contract: ScriptContract): Draft {
  return {
    displayName: contract.display_name ?? "",
    description: contract.description ?? "",
    category: contract.category ?? "",
    tags: (contract.tags ?? []).join(", "),
  };
}

// inputOf renders the draft as the request. Every field is sent, including the
// empty ones: this form shows all four, so an empty box here means the author
// cleared that field rather than declined to mention it.
function inputOf(draft: Draft): ScriptMetadataInput {
  return {
    display_name: draft.displayName.trim(),
    description: draft.description,
    category: draft.category.trim(),
    tags: splitTags(draft.tags),
  };
}

// splitTags turns the typed line into the list, dropping the empties a trailing
// comma leaves behind.
export function splitTags(line: string): string[] {
  return line
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => tag !== "");
}
