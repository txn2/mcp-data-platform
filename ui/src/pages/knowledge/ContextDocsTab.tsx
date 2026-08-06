import { useState } from "react";
import { ArrowLeft, Search, Plus, Pencil, Trash2, FileText } from "lucide-react";
import {
  useDocumentsBrowse,
  useDocumentsSearch,
  useDocument,
  useCreateDocument,
  useUpdateDocument,
  useDeleteDocument,
  documentId,
  MIN_SEARCH_LEN,
  type ContextDocument,
  type DocumentInput,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useAuthStore } from "@/stores/auth";
import { useDebounced } from "@/lib/useDebounced";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { ApiError } from "@/api/portal/client";
import { CancelButton } from "./catalog/primitives";
import { BackToList } from "./catalog/governance";

const MIN_SEARCH = MIN_SEARCH_LEN;

// Context documents attach only to these entity types upstream (mcp-datahub);
// the create form validates this client-side and the API rejects the rest.
const SUPPORTED_ENTITY_TYPES = ["dataset", "glossaryTerm", "glossaryNode", "container"];

function entityType(urn: string): string {
  const m = urn.match(/^urn:li:([^:]+):/);
  return m ? m[1]! : "";
}

type Mode =
  | { view: "list" }
  | { view: "doc"; id: string }
  | { view: "create" }
  | { view: "edit"; id: string };

/**
 * ContextDocsTab is the Context Docs tab of the Catalog section (#720, #1194):
 * browse/search DataHub context documents and manage them with full CRUD.
 * Create/edit/delete affordances appear only when the persona grants the
 * matching datahub tool and the connection is write-enabled; the API enforces
 * the same. The connection is chosen by CatalogSection, which renders this only
 * once one is selected.
 */
export function ContextDocsTab({ conn }: { conn: string }) {
  const [mode, setMode] = useState<Mode>({ view: "list" });
  const writable = useConnectionWritable(conn);
  const tools = useAuthStore((s) => s.user?.tools);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const has = (t: string) => (tools?.includes(t) ?? false) || isAdmin;
  const canCreate = writable && has("datahub_create");
  const canEdit = writable && has("datahub_update");
  const canDelete = writable && has("datahub_delete");

  return (
    <div className="space-y-4">
      {mode.view === "create" ? (
        <DocForm conn={conn} onDone={() => setMode({ view: "list" })} />
      ) : mode.view === "edit" ? (
        <DocForm conn={conn} editId={mode.id} onDone={(id) => setMode({ view: "doc", id: id ?? mode.id })} />
      ) : mode.view === "doc" ? (
        <DocDetail
          conn={conn}
          id={mode.id}
          canEdit={canEdit}
          canDelete={canDelete}
          onBack={() => setMode({ view: "list" })}
          onEdit={() => setMode({ view: "edit", id: mode.id })}
        />
      ) : (
        <DocList
          conn={conn}
          canCreate={canCreate}
          onOpen={(id) => setMode({ view: "doc", id })}
          onCreate={() => setMode({ view: "create" })}
        />
      )}
    </div>
  );
}

function DocList({
  conn,
  canCreate,
  onOpen,
  onCreate,
}: {
  conn: string;
  canCreate: boolean;
  onOpen: (id: string) => void;
  onCreate: () => void;
}) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const searching = debounced.trim().length >= MIN_SEARCH;
  const browse = useDocumentsBrowse(conn, { limit: 50 });
  const search = useDocumentsSearch(conn, debounced, { limit: 25 });
  const docs: ContextDocument[] = searching ? (search.data ?? []) : (browse.data?.documents ?? []);
  const active = searching ? search : browse;

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search context documents…"
            className="pl-9"
          />
        </div>
        {canCreate && (
          <Button onClick={onCreate}>
            <Plus /> New document
          </Button>
        )}
      </div>

      {active.isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load context documents.</AlertDescription>
        </Alert>
      ) : active.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-14 rounded-lg" />
          ))}
        </div>
      ) : docs.length === 0 ? (
        <EmptyState>
          {searching ? "No documents match your search." : "No context documents in this connection yet."}
        </EmptyState>
      ) : (
        <ul className="space-y-2">
          {docs.map((d) => (
            <li key={d.urn}>
              <button
                onClick={() => onOpen(documentId(d.urn))}
                className="flex w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
              >
                <span className="flex items-center gap-2 text-sm font-medium">
                  <FileText className="h-4 w-4 text-muted-foreground" />
                  {d.title || documentId(d.urn)}
                  {d.sub_type && (
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                      {d.sub_type}
                    </span>
                  )}
                </span>
                {d.snippet && <span className="line-clamp-2 text-xs text-muted-foreground">{d.snippet}</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function DocDetail({
  conn,
  id,
  canEdit,
  canDelete,
  onBack,
  onEdit,
}: {
  conn: string;
  id: string;
  canEdit: boolean;
  canDelete: boolean;
  onBack: () => void;
  onEdit: () => void;
}) {
  const { data: doc, isLoading, isError } = useDocument(conn, id);
  const del = useDeleteDocument(conn);
  const [confirming, setConfirming] = useState(false);

  return (
    <div className="space-y-4">
      {isError || !doc ? (
        <>
          <BackToList label="Back to documents" onBack={onBack} />
          {isLoading ? (
            <Skeleton className="h-40 rounded-lg" />
          ) : (
            <p className="text-sm text-destructive">Context document not found.</p>
          )}
        </>
      ) : (
        <>
          <PageHeader
            backLabel="Back to documents"
            onBack={onBack}
            title={doc.title}
            subtitle={doc.sub_type}
            actions={
              <>
                {canEdit && (
                  <Button variant="outline" size="sm" onClick={onEdit}>
                    <Pencil /> Edit
                  </Button>
                )}
                {canDelete &&
                  (confirming ? (
                    <>
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => del.mutate(id, { onSuccess: onBack })}
                        disabled={del.isPending}
                      >
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
                      <Trash2 /> Delete
                    </Button>
                  ))}
              </>
            }
          />
          {del.isError && <p className="text-xs text-destructive">Delete failed.</p>}
          {doc.related_asset_urns && doc.related_asset_urns.length > 0 && (
            <p className="text-xs text-muted-foreground">
              Attached to: {doc.related_asset_urns.join(", ")}
            </p>
          )}
          <div className="rounded-lg border p-4">
            <MarkdownRenderer content={doc.body ?? ""} />
          </div>
        </>
      )}
    </div>
  );
}

function DocForm({
  conn,
  editId,
  onDone,
}: {
  conn: string;
  editId?: string;
  onDone: (id?: string) => void;
}) {
  const existing = useDocument(conn, editId ?? null);
  const create = useCreateDocument(conn);
  const update = useUpdateDocument(conn);
  const isEdit = !!editId;

  const [title, setTitle] = useState("");
  const [category, setCategory] = useState("");
  const [entityUrn, setEntityUrn] = useState("");
  const [body, setBody] = useState("");
  const [seeded, setSeeded] = useState(false);

  // Seed the form from the loaded document on edit.
  if (isEdit && existing.data && !seeded) {
    setTitle(existing.data.title);
    setCategory(existing.data.sub_type ?? "");
    setBody(existing.data.body ?? "");
    setSeeded(true);
  }

  // In edit mode, wait for the document to load so the user never sees or saves
  // blank fields over real content, and surface a load failure rather than
  // presenting an empty form the user could save over the real document.
  if (isEdit && existing.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  if (isEdit && (existing.isError || !existing.data)) {
    return (
      <div className="space-y-3">
        <Alert variant="destructive">
          <AlertDescription>Failed to load this document.</AlertDescription>
        </Alert>
        <button onClick={() => onDone()} className="text-sm text-primary hover:underline">
          Go back
        </button>
      </div>
    );
  }

  const entityBad = !isEdit && entityUrn.trim() !== "" && !SUPPORTED_ENTITY_TYPES.includes(entityType(entityUrn.trim()));
  const mut = isEdit ? update : create;
  const canSubmit = title.trim() !== "" && (isEdit || (entityUrn.trim() !== "" && !entityBad));

  const submit = () => {
    const input: DocumentInput = { title: title.trim(), content: body, category: category.trim() || undefined };
    if (isEdit) {
      update.mutate({ id: editId!, ...input }, { onSuccess: (d) => onDone(documentId(d.urn)) });
    } else {
      create.mutate(
        { ...input, entity_urn: entityUrn.trim() },
        { onSuccess: (d) => onDone(documentId(d.urn)) },
      );
    }
  };

  return (
    <div className="space-y-4">
      <button onClick={() => onDone()} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground">
        <ArrowLeft className="size-4" /> Cancel
      </button>
      <h2 className="text-lg font-semibold">{isEdit ? "Edit context document" : "New context document"}</h2>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="doc-title">Title</Label>
          <Input id="doc-title" value={title} onChange={(e) => setTitle(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="doc-category">Category</Label>
          <Input
            id="doc-category"
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            placeholder="e.g. runbook, note"
          />
        </div>
      </div>

      {!isEdit && (
        <div className="space-y-1.5">
          <Label htmlFor="doc-entity">Attach to entity</Label>
          <Input
            id="doc-entity"
            value={entityUrn}
            onChange={(e) => setEntityUrn(e.target.value)}
            placeholder="urn:li:dataset:(...) or urn:li:glossaryTerm:… / glossaryNode / container URN"
          />
          {entityBad ? (
            <span className="text-xs text-destructive">
              Context documents attach only to Dataset, GlossaryTerm, GlossaryNode, or Container entities.
            </span>
          ) : (
            <span className="text-xs text-muted-foreground">
              The entity this document documents. Cannot be changed after creation.
            </span>
          )}
        </div>
      )}

      <div className="space-y-1">
        <span className="text-sm font-medium">Content</span>
        <MarkdownEditor value={body} onChange={setBody} minHeight="360px" placeholder="Write the context document in markdown…" />
      </div>

      {mut.isError && (
        <p className="text-sm text-destructive">
          {mut.error instanceof ApiError ? mut.error.detail : "Save failed."}
        </p>
      )}

      <div className="flex gap-2">
        <Button onClick={submit} disabled={!canSubmit || mut.isPending}>
          {isEdit ? "Save changes" : "Create document"}
        </Button>
        <CancelButton onClick={() => onDone()} />
      </div>
    </div>
  );
}
