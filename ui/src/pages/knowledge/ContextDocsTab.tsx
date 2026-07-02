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
import { DataHubConnectionSelect, useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { useDebounced } from "@/lib/useDebounced";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { ApiError } from "@/api/portal/client";

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
 * ContextDocsTab is the Knowledge > Context Docs sub-tab (#720): browse/search
 * DataHub context documents and manage them with full CRUD. Create/edit/delete
 * affordances appear only when the persona grants the matching datahub tool and
 * the connection is write-enabled; the API enforces the same.
 */
export function ContextDocsTab({ conn, onConnChange }: { conn: string; onConnChange: (c: string) => void }) {
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
      <DataHubConnectionSelect value={conn} onChange={onConnChange} />
      {!conn ? null : mode.view === "create" ? (
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
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search context documents…"
            className="w-full rounded-md border bg-background py-2 pl-9 pr-3 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        {canCreate && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" /> New document
          </button>
        )}
      </div>

      {active.isError ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load context documents.
        </p>
      ) : active.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-14 animate-pulse rounded-lg border bg-muted/40" />
          ))}
        </div>
      ) : docs.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {searching ? "No documents match your search." : "No context documents in this connection yet."}
        </p>
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
      <button onClick={onBack} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" /> Back to documents
      </button>

      {isError || !doc ? (
        isLoading ? (
          <div className="h-40 animate-pulse rounded-lg border bg-muted/40" />
        ) : (
          <p className="text-sm text-destructive">Context document not found.</p>
        )
      ) : (
        <>
          <div className="flex items-start justify-between gap-4">
            <div>
              <h2 className="text-lg font-semibold">{doc.title}</h2>
              {doc.sub_type && <p className="text-xs text-muted-foreground">{doc.sub_type}</p>}
            </div>
            <div className="flex shrink-0 gap-2">
              {canEdit && (
                <button
                  onClick={onEdit}
                  className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
                >
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </button>
              )}
              {canDelete &&
                (confirming ? (
                  <span className="flex items-center gap-1.5 text-sm">
                    <button
                      onClick={() => del.mutate(id, { onSuccess: onBack })}
                      disabled={del.isPending}
                      className="rounded-md bg-destructive px-3 py-1.5 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
                    >
                      Confirm delete
                    </button>
                    <button onClick={() => setConfirming(false)} className="rounded-md border px-3 py-1.5 hover:bg-muted">
                      Cancel
                    </button>
                  </span>
                ) : (
                  <button
                    onClick={() => setConfirming(true)}
                    className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10"
                  >
                    <Trash2 className="h-3.5 w-3.5" /> Delete
                  </button>
                ))}
            </div>
          </div>
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
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load this document.
        </p>
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
      <button onClick={() => onDone()} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" /> Cancel
      </button>
      <h2 className="text-lg font-semibold">{isEdit ? "Edit context document" : "New context document"}</h2>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="space-y-1">
          <span className="text-sm font-medium">Title</span>
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
        </label>
        <label className="space-y-1">
          <span className="text-sm font-medium">Category</span>
          <input
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            placeholder="e.g. runbook, note"
            className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
        </label>
      </div>

      {!isEdit && (
        <label className="space-y-1">
          <span className="text-sm font-medium">Attach to entity</span>
          <input
            value={entityUrn}
            onChange={(e) => setEntityUrn(e.target.value)}
            placeholder="urn:li:dataset:(...) or urn:li:glossaryTerm:… / glossaryNode / container URN"
            className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
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
        </label>
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
        <button
          onClick={submit}
          disabled={!canSubmit || mut.isPending}
          className="rounded-md bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {isEdit ? "Save changes" : "Create document"}
        </button>
        <button onClick={() => onDone()} className="rounded-md border px-4 py-1.5 text-sm hover:bg-muted">
          Cancel
        </button>
      </div>
    </div>
  );
}
