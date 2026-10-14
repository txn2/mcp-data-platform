import { useState } from "react";
import { FileText, Pencil, Trash2 } from "lucide-react";
import { useWriteAspect, type ContextDocument } from "@/api/portal/catalog";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AddButton, CancelButton, MutationError, SaveButton } from "./primitives";

/** newDocumentID names a context document the moment it is written. */
function newDocumentID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `doc-${Date.now().toString(36)}`;
}

/**
 * ContextDocuments lists the markdown notes attached to a dataset (the
 * context_documents aspect) and, for an editor, adds, edits and removes them.
 * Each change rewrites the aspect, so the dataset's history holds every
 * version of every note.
 */
export function ContextDocuments({
  reference,
  read,
  canEdit,
}: {
  reference: string;
  // read is the aspect as the page read it: undefined when absent.
  read: ContextDocument[] | undefined;
  canEdit: boolean;
}) {
  const write = useWriteAspect();
  const documents = read ?? [];
  // editing is the id of the document being edited, "new" for one being
  // added, or null.
  const [editing, setEditing] = useState<string | null>(null);
  const save = (next: ContextDocument[]) =>
    write.mutate(
      { reference, aspect: "context_documents", value: next.length === 0 ? null : next, expect: read ?? null },
      { onSuccess: () => setEditing(null) },
    );

  return (
    <SectionCard
      title={
        <span className="flex items-center gap-1.5">
          <FileText className="size-4" /> Context documents
        </span>
      }
      action={
        canEdit &&
        editing === null && <AddButton label="Add document" onClick={() => setEditing("new")} />
      }
    >
      <div className="space-y-3">
        {documents.length === 0 && editing !== "new" && (
          <p className="text-sm text-muted-foreground">No context documents.</p>
        )}
        {documents.map((d) =>
          editing === d.id ? (
            <DocumentForm
              key={d.id}
              initial={d}
              pending={write.isPending}
              onCancel={() => setEditing(null)}
              onSave={(doc) => save(documents.map((x) => (x.id === d.id ? doc : x)))}
            />
          ) : (
            <article key={d.id} className="rounded-lg border p-3">
              <header className="mb-1 flex items-center gap-2">
                <h4 className="text-sm font-medium">{d.title}</h4>
                {d.category && (
                  <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                    {d.category}
                  </span>
                )}
                {canEdit && editing === null && (
                  <span className="ml-auto flex gap-1">
                    <Button
                      variant="ghost"
                      size="xs"
                      aria-label={`Edit ${d.title}`}
                      onClick={() => setEditing(d.id)}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      variant="ghost"
                      size="xs"
                      aria-label={`Remove ${d.title}`}
                      disabled={write.isPending}
                      onClick={() => save(documents.filter((x) => x.id !== d.id))}
                    >
                      <Trash2 />
                    </Button>
                  </span>
                )}
              </header>
              <CollapsibleMarkdown content={d.content} fadeFrom="from-card" />
            </article>
          ),
        )}
        {editing === "new" && (
          <DocumentForm
            initial={{ id: newDocumentID(), title: "", content: "", content_type: "text/markdown" }}
            pending={write.isPending}
            onCancel={() => setEditing(null)}
            onSave={(doc) => save([...documents, doc])}
          />
        )}
        <MutationError mut={write} />
      </div>
    </SectionCard>
  );
}

function DocumentForm({
  initial,
  pending,
  onSave,
  onCancel,
}: {
  initial: ContextDocument;
  pending: boolean;
  onSave: (d: ContextDocument) => void;
  onCancel: () => void;
}) {
  const [title, setTitle] = useState(initial.title);
  const [category, setCategory] = useState(initial.category ?? "");
  const [content, setContent] = useState(initial.content);
  return (
    <div className="space-y-2 rounded-lg border p-3">
      <div className="flex flex-wrap gap-2">
        <Input
          aria-label="Document title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Title"
          className="h-8 flex-1 text-sm"
        />
        <Input
          aria-label="Document category"
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          placeholder="Category"
          className="h-8 w-40 text-sm"
        />
      </div>
      <MarkdownEditor
        value={content}
        onChange={setContent}
        label="Document content"
        minHeight="200px"
        placeholder="Write the note in markdown…"
      />
      <div className="flex gap-2">
        <SaveButton
          disabled={pending || !title.trim()}
          onClick={() =>
            onSave({
              ...initial,
              title: title.trim(),
              category: category.trim() || undefined,
              content,
            })
          }
        />
        <CancelButton onClick={onCancel} />
      </div>
    </div>
  );
}
