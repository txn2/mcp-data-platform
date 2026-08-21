import { useState } from "react";
import { useSaveScriptMetadata } from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptMetadataInput } from "@/api/portal/hooks/scripts";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { SectionCard } from "@/components/patterns/SectionCard";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { descriptionSummary } from "./descriptionSummary";
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
// here: an author documenting their own script should not have to ask an
// agent to do it on a page they are already looking at. The save applies
// immediately and is captured as a version, because what a script claimed to
// do is part of explaining what one of its runs did.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** owned gates editing; a reader who does not own the script only reads. */
  owned: boolean;
}

export function ScriptDocumentation({ scriptId, contract, owned }: Props) {
  const [editing, setEditing] = useState(false);

  return (
    // Open by default and foldable (#1407): a description is a document, and a
    // long one is exactly the case for folding it — but a reader who opens a
    // script wants to know what it is, so the document is what they land on.
    <SectionCard
      title="About"
      collapsible
      summary={descriptionSummary(contract.description ?? "") || "No description"}
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
  onDone,
}: {
  scriptId: string;
  contract: ScriptContract;
  onDone: () => void;
}) {
  const save = useSaveScriptMetadata(scriptId);
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
