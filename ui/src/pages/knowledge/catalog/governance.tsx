import { useState } from "react";
import { ArrowLeft, Trash2, type LucideIcon } from "lucide-react";
import { useWriteAspect, type DefinitionSummary, type Use } from "@/api/portal/catalog";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { catalogHref } from "@/lib/entityRefs";
import { markdownToPlainText } from "@/lib/markdownText";
import { CancelButton, MutationError, SaveButton } from "./primitives";

// Shared surfaces for the catalog's definitions under Catalog: the glossary,
// tags, domains and data products. Each lists its entries, opens one to see what
// it means and which datasets carry it, and edits it through the same writes,
// so the mechanics live here once rather than per kind.

// PageCapNotice states that a read came back full, so a capped list is never
// presented as the whole set.
export function PageCapNotice({
  shown,
  limit,
  what,
  hint,
}: {
  shown: number;
  limit: number;
  what: string;
  hint: string;
}) {
  if (shown < limit) return null;
  return (
    <p className="text-xs text-muted-foreground">
      Showing the first {limit} {what}; there may be more. {hint}
    </p>
  );
}

// VocabCard is one entry in a definition list: its name, its description, and
// the click that opens it. An entry with no description says so rather than
// rendering an empty line, so an undocumented entry is visible as a gap to fill.
// Descriptions are markdown, so the two-line preview is flattened to plain text.
export function VocabCard({
  entry,
  icon: Icon,
  onOpen,
  trailing,
}: {
  entry: DefinitionSummary;
  icon: LucideIcon;
  onOpen: () => void;
  trailing?: React.ReactNode;
}) {
  return (
    <button
      onClick={onOpen}
      className="flex h-full w-full flex-col gap-1 rounded-xl border bg-card p-3 text-left shadow-sm transition-colors hover:border-primary/50 hover:bg-muted/50"
    >
      <span className="flex items-center gap-2 text-sm font-medium">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        {entry.name}
        {trailing}
      </span>
      {entry.description ? (
        <span className="line-clamp-2 text-xs text-muted-foreground">
          {markdownToPlainText(entry.description)}
        </span>
      ) : (
        <span className="text-xs italic text-muted-foreground">No description</span>
      )}
    </button>
  );
}

// UseLink renders one dataset or column carrying a definition. It deep-links
// into the Tables tab through the shared catalogHref, and stays a plain row when
// there is no navigator, so it is never styled as a link it cannot follow.
export function UseLink({ use, onNavigate }: { use: Use; onNavigate?: (path: string) => void }) {
  const href = catalogHref(use.dataset);
  const body = (
    <>
      <span className="text-sm font-medium">
        {use.name}
        {use.column && <span className="font-mono text-muted-foreground">.{use.column}</span>}
      </span>
      <span className="text-xs text-muted-foreground">{use.connection}</span>
    </>
  );
  const shell = "flex flex-col gap-0.5 rounded-lg border p-3";
  if (!href || !onNavigate) return <div className={shell}>{body}</div>;
  return (
    <a
      href={href}
      onClick={(e) => {
        e.preventDefault();
        onNavigate(href);
      }}
      className={`${shell} transition-colors hover:border-primary/50 hover:bg-muted/50`}
    >
      {body}
    </a>
  );
}

// EntityDescription renders a catalog entity's description and, for an editor,
// the markdown edit form. The save writes the entity's description aspect, the
// one write a dataset, a column and every definition kind share.
export function EntityDescription({
  reference,
  value,
  canEdit,
  label,
}: {
  reference: string;
  value: string;
  canEdit: boolean;
  label: string;
}) {
  const write = useWriteAspect();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);

  return (
    <SectionCard
      title="Description"
      action={
        canEdit &&
        !editing && (
          <Button
            variant="ghost"
            size="xs"
            className="text-muted-foreground"
            onClick={() => {
              setDraft(value);
              setEditing(true);
            }}
          >
            Edit description
          </Button>
        )
      }
    >
      {editing ? (
        <div className="space-y-2">
          <MarkdownEditor
            value={draft}
            onChange={setDraft}
            label={label}
            minHeight="240px"
            placeholder="Describe this in markdown…"
          />
          <div className="flex gap-2">
            <SaveButton
              disabled={write.isPending}
              onClick={() =>
                write.mutate(
                  {
                    reference,
                    aspect: "description",
                    value: draft.trim() === "" ? null : draft.trim(),
                    expect: value === "" ? null : value,
                  },
                  { onSuccess: () => setEditing(false) },
                )
              }
            />
            <CancelButton onClick={() => setEditing(false)} />
          </div>
          <MutationError mut={write} />
        </div>
      ) : value ? (
        <MarkdownRenderer content={value} bare />
      ) : (
        <p className="text-sm italic text-muted-foreground">No description</p>
      )}
    </SectionCard>
  );
}

// DeleteControl removes a definition behind a confirmation that states what
// the delete does first. The platform refuses a delete of a definition still in
// use, and the refusal is shown as the mutation's error.
export function DeleteControl({
  label,
  impact,
  mut,
  onConfirm,
}: {
  label: string;
  impact: React.ReactNode;
  mut: { isPending: boolean; isError: boolean; error: unknown };
  onConfirm: () => void;
}) {
  const [confirming, setConfirming] = useState(false);

  return (
    <div className="space-y-2">
      <div className="flex justify-end gap-2">
        {confirming ? (
          <>
            <Button variant="destructive" size="sm" onClick={onConfirm} disabled={mut.isPending}>
              Confirm delete
            </Button>
            <CancelButton onClick={() => setConfirming(false)} />
          </>
        ) : (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setConfirming(true)}
            className="text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 /> {label}
          </Button>
        )}
      </div>
      {confirming && (
        <Alert variant="destructive" className="border-destructive/40 bg-destructive/10">
          <AlertDescription className="text-foreground">{impact}</AlertDescription>
        </Alert>
      )}
      <MutationError mut={mut} />
    </div>
  );
}

// BackToList is the return affordance a deep-linked view needs: the reader may
// have arrived from a knowledge page rather than from the list, so there is no
// browsing history within the tab to go back through.
export function BackToList({ label, onBack }: { label: string; onBack: () => void }) {
  return (
    <button
      onClick={onBack}
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="h-4 w-4" /> {label}
    </button>
  );
}
