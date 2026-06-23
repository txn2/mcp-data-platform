import { useState } from "react";
import { BookOpen, Search, Plus, Pencil, Trash2, ArrowLeft, History, X } from "lucide-react";
import {
  useKnowledgePages,
  useSearchKnowledgePages,
  useKnowledgePage,
  useKnowledgePageVersions,
  useCreateKnowledgePage,
  useUpdateKnowledgePage,
  useDeleteKnowledgePage,
} from "@/api/portal/hooks";
import type { KnowledgePage, KnowledgePageInput } from "@/api/portal/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { useAuthStore } from "@/stores/auth";

type Mode = { view: "list" } | { view: "page"; id: string } | { view: "edit"; id: string } | { view: "create" };

/**
 * KnowledgePagesPage is the portal home for canonical business/domain knowledge
 * pages. Everyone can browse and read; create/edit/remove is shown only to
 * personas with apply_knowledge access (admins). It reuses the shared
 * MarkdownEditor and MarkdownRenderer.
 */
export function KnowledgePagesPage() {
  const [mode, setMode] = useState<Mode>({ view: "list" });
  const canEdit = useAuthStore((s) => s.isAdmin());

  if (mode.view === "create") {
    return <KnowledgePageForm key="create" onDone={(id) => setMode(id ? { view: "page", id } : { view: "list" })} />;
  }
  if (mode.view === "edit") {
    // key by id so switching edit targets always remounts with fresh hydration.
    return <KnowledgePageForm key={mode.id} id={mode.id} onDone={(id) => setMode({ view: "page", id: id ?? mode.id })} />;
  }
  if (mode.view === "page") {
    return (
      <KnowledgePageDetail
        id={mode.id}
        canEdit={canEdit}
        onBack={() => setMode({ view: "list" })}
        onEdit={() => setMode({ view: "edit", id: mode.id })}
        onDeleted={() => setMode({ view: "list" })}
      />
    );
  }
  return <KnowledgePageList canEdit={canEdit} onOpen={(id) => setMode({ view: "page", id })} onCreate={() => setMode({ view: "create" })} />;
}

function KnowledgePageList({ canEdit, onOpen, onCreate }: { canEdit: boolean; onOpen: (id: string) => void; onCreate: () => void }) {
  const [query, setQuery] = useState("");
  const trimmed = query.trim();
  const list = useKnowledgePages(undefined);
  const search = useSearchKnowledgePages(trimmed, { limit: 25 });

  const pages: KnowledgePage[] = trimmed
    ? (search.data ?? []).map((s) => s.page)
    : (list.data?.pages ?? []);
  const loading = trimmed ? search.isLoading : list.isLoading;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2 text-lg font-semibold text-foreground">
          <BookOpen className="h-5 w-5 text-primary" />
          Knowledge Pages
        </div>
        {canEdit && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            <Plus className="h-4 w-4" /> New page
          </button>
        )}
      </div>

      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search knowledge by content..."
          className="w-full rounded-md border border-border bg-background py-2 pl-9 pr-3 text-sm outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>

      {(trimmed ? search.isError : list.isError) ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load knowledge pages. Please try again.
        </p>
      ) : loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : pages.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
          {trimmed ? "No knowledge pages match your search." : "No knowledge pages yet."}
          {canEdit && !trimmed && (
            <div className="mt-3">
              <button onClick={onCreate} className="text-primary hover:underline">
                Create the first page
              </button>
            </div>
          )}
        </div>
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {pages.map((p) => (
            <li key={p.id}>
              <button
                onClick={() => onOpen(p.id)}
                className="flex h-full w-full flex-col rounded-lg border border-border bg-card p-4 text-left transition hover:border-primary/50 hover:shadow-sm"
              >
                <span className="font-medium text-foreground">{p.title}</span>
                {p.summary && <span className="mt-1 line-clamp-3 text-sm text-muted-foreground">{p.summary}</span>}
                {p.tags.length > 0 && (
                  <span className="mt-3 flex flex-wrap gap-1">
                    {p.tags.map((t) => (
                      <span key={t} className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                        {t}
                      </span>
                    ))}
                  </span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function KnowledgePageDetail({
  id,
  canEdit,
  onBack,
  onEdit,
  onDeleted,
}: {
  id: string;
  canEdit: boolean;
  onBack: () => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const { data: page, isLoading, isError } = useKnowledgePage(id);
  const del = useDeleteKnowledgePage();
  const [showHistory, setShowHistory] = useState(false);

  if (isLoading) return <p className="text-sm text-muted-foreground">Loading...</p>;
  if (isError || !page) return <p className="text-sm text-destructive">Knowledge page not found.</p>;

  const handleDelete = () => {
    if (!window.confirm(`Remove "${page.title}"? It will no longer appear in search.`)) return;
    del.mutate(id, { onSuccess: onDeleted });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <button onClick={onBack} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" /> All pages
        </button>
        {canEdit && (
          <div className="flex items-center gap-2">
            <button
              onClick={() => setShowHistory((v) => !v)}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
            >
              <History className="h-4 w-4" /> History
            </button>
            <button
              onClick={onEdit}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm hover:bg-muted"
            >
              <Pencil className="h-4 w-4" /> Edit
            </button>
            <button
              onClick={handleDelete}
              disabled={del.isPending}
              className="inline-flex items-center gap-1.5 rounded-md border border-border px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
            >
              <Trash2 className="h-4 w-4" /> Remove
            </button>
          </div>
        )}
      </div>

      <div>
        <h1 className="text-2xl font-semibold text-foreground">{page.title}</h1>
        {page.summary && <p className="mt-1 text-muted-foreground">{page.summary}</p>}
        <p className="mt-2 text-xs text-muted-foreground">
          v{page.current_version}
          {page.updated_by ? ` · last edited by ${page.updated_by}` : ""}
        </p>
      </div>

      {showHistory && <KnowledgePageHistory id={id} onClose={() => setShowHistory(false)} />}

      <article
        className="prose prose-sm max-w-none rounded-lg border border-border bg-card p-6 dark:prose-invert"
        data-feedback-anchorable
      >
        <MarkdownRenderer content={page.body} />
      </article>
    </div>
  );
}

function KnowledgePageHistory({ id, onClose }: { id: string; onClose: () => void }) {
  const { data } = useKnowledgePageVersions(id);
  return (
    <div className="rounded-lg border border-border bg-muted/40 p-4">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-medium text-foreground">Version history</span>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground">
          <X className="h-4 w-4" />
        </button>
      </div>
      <ul className="space-y-1 text-sm text-muted-foreground">
        {(data?.versions ?? []).map((v) => (
          <li key={v.id} className="flex justify-between gap-4">
            <span>
              v{v.version}
              {v.change_summary ? `: ${v.change_summary}` : ""}
            </span>
            <span className="shrink-0 text-xs">{new Date(v.created_at).toLocaleString()}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function KnowledgePageForm({ id, onDone }: { id?: string; onDone: (id: string | null) => void }) {
  const existing = useKnowledgePage(id ?? null);
  const create = useCreateKnowledgePage();
  const update = useUpdateKnowledgePage();

  const loaded = id ? existing.data : undefined;
  const [title, setTitle] = useState("");
  const [summary, setSummary] = useState("");
  const [body, setBody] = useState("");
  const [tags, setTags] = useState("");
  const [hydrated, setHydrated] = useState(!id);
  const [error, setError] = useState<string | null>(null);

  // Hydrate the form once the existing page loads (edit mode).
  if (id && loaded && !hydrated) {
    setTitle(loaded.title);
    setSummary(loaded.summary ?? "");
    setBody(loaded.body);
    setTags(loaded.tags.join(", "));
    setHydrated(true);
  }

  const pending = create.isPending || update.isPending;

  // In edit mode, do not render the form until the page has loaded, so the
  // user never sees (or saves) blank fields over real content.
  if (id && existing.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }
  if (id && existing.isError) {
    return (
      <div className="space-y-3">
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">Failed to load this page.</p>
        <button onClick={() => onDone(id)} className="text-sm text-primary hover:underline">
          Go back
        </button>
      </div>
    );
  }

  const submit = () => {
    setError(null);
    if (!title.trim()) {
      setError("Title is required.");
      return;
    }
    const input: KnowledgePageInput = {
      title: title.trim(),
      summary: summary.trim(),
      body,
      tags: tags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean),
    };
    const onErr = (e: unknown) => setError(e instanceof Error ? e.message : "Save failed.");
    if (id) {
      update.mutate({ id, input }, { onSuccess: (p) => onDone(p.id), onError: onErr });
    } else {
      create.mutate(input, { onSuccess: (p) => onDone(p.id), onError: onErr });
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <button onClick={() => onDone(id ?? null)} className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
          <ArrowLeft className="h-4 w-4" /> Cancel
        </button>
        <button
          onClick={submit}
          disabled={pending}
          className="rounded-md bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
        >
          {pending ? "Saving..." : id ? "Save changes" : "Create page"}
        </button>
      </div>

      {error && <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>}

      <input
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder="Title"
        className="w-full rounded-md border border-border bg-background px-3 py-2 text-lg font-medium outline-none focus:ring-2 focus:ring-primary/40"
      />
      <input
        value={summary}
        onChange={(e) => setSummary(e.target.value)}
        placeholder="One-line summary (optional)"
        className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/40"
      />
      <input
        value={tags}
        onChange={(e) => setTags(e.target.value)}
        placeholder="Tags, comma-separated (optional)"
        className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/40"
      />
      <MarkdownEditor value={body} onChange={setBody} minHeight="420px" placeholder="Write the knowledge page in markdown..." />
    </div>
  );
}
