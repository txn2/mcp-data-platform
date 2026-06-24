import { useEffect, useMemo, useState } from "react";
import { Search, Plus, Pencil, Trash2, ArrowLeft, History, X, MessageSquare } from "lucide-react";
import {
  useKnowledgePages,
  useSearchKnowledgePages,
  useKnowledgePage,
  useResolveRefs,
  useKnowledgePageVersions,
  useCreateKnowledgePage,
  useUpdateKnowledgePage,
  useDeleteKnowledgePage,
  useThreadCounts,
} from "@/api/portal/hooks";
import type { KnowledgePage, KnowledgePageInput } from "@/api/portal/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { extractRefUrns } from "@/lib/entityRefs";
import { RelatedPanel } from "@/components/knowledge/RelatedPanel";
import { RefPicker } from "@/components/knowledge/RefPicker";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { useAuthStore } from "@/stores/auth";
import { parseTags } from "@/lib/tags";
import { FilterChip } from "@/components/FilterChip";
import { useDebounced } from "@/lib/useDebounced";
import { MIN_SEARCH_LEN } from "@/api/portal/hooks";

type Mode = { view: "list" } | { view: "page"; id: string } | { view: "edit"; id: string } | { view: "create" };

/**
 * KnowledgePagesPage is the portal home for canonical business/domain knowledge
 * pages. Everyone can browse and read; create/edit/remove is shown only to
 * personas with apply_knowledge access (admins). It reuses the shared
 * MarkdownEditor and MarkdownRenderer.
 */
export function KnowledgePagesPage({
  openPage,
  onPageOpened,
  onNavigate,
}: {
  // A request from the Knowledge hub's search to open a specific page in detail
  // view. The bump counter makes re-opening the same page re-fire the effect.
  openPage?: { id: string; n: number };
  // Called once the request has been consumed so the parent can clear it,
  // preventing a stale request from re-opening the page on the next remount.
  onPageOpened?: () => void;
  // Navigate to an in-app path (for entity-reference chip deep-links).
  onNavigate?: (path: string) => void;
} = {}) {
  const [mode, setMode] = useState<Mode>({ view: "list" });

  useEffect(() => {
    if (openPage) {
      setMode({ view: "page", id: openPage.id });
      onPageOpened?.();
    }
  }, [openPage, onPageOpened]);

  // Create/edit/remove gates on the apply_knowledge capability (a tool-access
  // gate, not an admin-role gate), or admin. This mirrors the REST handler's
  // userHasToolAccess (pkg/portal/knowledge_page_handler.go): the capability
  // grants non-admins, and admins are allowed too since apply_knowledge may be
  // unregistered on a deployment (#661).
  const canEdit = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );

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
        onNavigate={onNavigate}
        onBack={() => setMode({ view: "list" })}
        onEdit={() => setMode({ view: "edit", id: mode.id })}
        onDeleted={() => setMode({ view: "list" })}
      />
    );
  }
  return <KnowledgePageList canEdit={canEdit} onOpen={(id) => setMode({ view: "page", id })} onCreate={() => setMode({ view: "create" })} />;
}

function PageCard({
  page,
  openThreads,
  onOpen,
}: {
  page: KnowledgePage;
  openThreads: number;
  onOpen: (id: string) => void;
}) {
  return (
    <button
      onClick={() => onOpen(page.id)}
      className="flex h-full w-full flex-col rounded-lg border border-border bg-card p-4 text-left transition hover:border-primary/50 hover:shadow-sm"
    >
      <span className="flex items-start justify-between gap-2">
        <span className="font-medium text-foreground">{page.title}</span>
        {openThreads > 0 && (
          <span
            className="inline-flex shrink-0 items-center gap-1 rounded-full bg-muted px-1.5 py-0.5 text-[11px] font-medium text-muted-foreground"
            title={`${openThreads} open feedback ${openThreads === 1 ? "thread" : "threads"}`}
          >
            <MessageSquare className="h-3 w-3" />
            {openThreads}
          </span>
        )}
      </span>
      {page.summary && (
        <span className="mt-1 line-clamp-3 text-sm text-muted-foreground">{page.summary}</span>
      )}
      {page.tags.length > 0 && (
        <span className="mt-3 flex flex-wrap gap-1">
          {page.tags.map((t) => (
            <span key={t} className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
              {t}
            </span>
          ))}
        </span>
      )}
      <span className="mt-auto pt-3 text-[11px] text-muted-foreground">
        Updated {new Date(page.updated_at).toLocaleDateString()}
        {page.updated_by ? ` by ${page.updated_by}` : ""}
      </span>
    </button>
  );
}

function KnowledgePageList({ canEdit, onOpen, onCreate }: { canEdit: boolean; onOpen: (id: string) => void; onCreate: () => void }) {
  const [query, setQuery] = useState("");
  const [tag, setTag] = useState("");
  // Debounce the input and require a minimum length before searching, so the
  // content search issues one request after the user pauses rather than one per
  // keystroke. The hook enforces the same floor as a backstop.
  const debouncedQuery = useDebounced(query, 250);
  const trimmed = debouncedQuery.trim();
  const searching = trimmed.length >= MIN_SEARCH_LEN;
  // A high limit so the tag facet and counts reflect the whole knowledgebase,
  // not just the first page of results.
  const list = useKnowledgePages({ limit: 200 });
  const search = useSearchKnowledgePages(trimmed, { limit: 25 });

  const allPages = useMemo(() => list.data?.pages ?? [], [list.data]);
  const total = list.data?.total ?? allPages.length;

  // Tag facet (tag -> count), most-used first, derived from the loaded pages.
  const tagCounts = useMemo(() => {
    const m = new Map<string, number>();
    for (const p of allPages) for (const t of p.tags) m.set(t, (m.get(t) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  }, [allPages]);

  // Browse list: filter by the selected tag, newest first.
  const browsePages = useMemo(() => {
    const filtered = tag ? allPages.filter((p) => p.tags.includes(tag)) : allPages;
    return [...filtered].sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  }, [allPages, tag]);

  const pages: KnowledgePage[] = searching
    ? (search.data ?? []).map((s) => s.page)
    : browsePages;
  const loading = searching ? search.isLoading : list.isLoading;

  // Open-feedback-thread counts for the visible pages, so each card can badge
  // pages that have feedback awaiting attention.
  const pageIds = useMemo(() => pages.map((p) => p.id), [pages]);
  const threadCounts = useThreadCounts("knowledge_page", pageIds);

  return (
    <div className="space-y-4">
      {/* Count + create */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          <span className="font-semibold text-foreground">{total}</span>{" "}
          {total === 1 ? "knowledge page" : "knowledge pages"}
          {!searching && tag && (
            <>
              {" "}
              tagged <span className="font-medium text-foreground">{tag}</span>
            </>
          )}
        </p>
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

      {/* Tag browse (browse mode only) */}
      {!searching && tagCounts.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <FilterChip label="All" active={tag === ""} onClick={() => setTag("")} />
          {tagCounts.map(([t, c]) => (
            <FilterChip
              key={t}
              label={t}
              count={c}
              active={tag === t}
              onClick={() => setTag(tag === t ? "" : t)}
            />
          ))}
        </div>
      )}

      {(searching ? search.isError : list.isError) ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load knowledge pages. Please try again.
        </p>
      ) : loading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : pages.length === 0 ? (
        <div className="rounded-lg border border-dashed border-border p-10 text-center text-sm text-muted-foreground">
          {searching
            ? "No knowledge pages match your search."
            : tag
              ? `No pages tagged "${tag}".`
              : "No knowledge pages yet."}
          {canEdit && !searching && !tag && (
            <div className="mt-3">
              <button onClick={onCreate} className="text-primary hover:underline">
                Create the first page
              </button>
            </div>
          )}
        </div>
      ) : (
        <>
          {!searching && (
            <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
              {tag ? `Tagged ${tag}` : "Recently updated"}
            </p>
          )}
          <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {pages.map((p) => (
              <li key={p.id}>
                <PageCard
                  page={p}
                  openThreads={threadCounts.data?.[p.id] ?? 0}
                  onOpen={onOpen}
                />
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function KnowledgePageDetail({
  id,
  canEdit,
  onNavigate,
  onBack,
  onEdit,
  onDeleted,
}: {
  id: string;
  canEdit: boolean;
  onNavigate?: (path: string) => void;
  onBack: () => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const { data: page, isLoading, isError } = useKnowledgePage(id);
  const del = useDeleteKnowledgePage();
  const [showHistory, setShowHistory] = useState(false);
  // Resolve the body's inline entity references to display names for the chips (#664).
  const refUrns = useMemo(() => extractRefUrns(page?.body ?? ""), [page?.body]);
  const { data: resolvedRefs } = useResolveRefs(refUrns);

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
        <div className="flex items-center gap-2">
          {/* Feedback is open to any authenticated user; apply_knowledge holders
              (canEdit) also moderate. */}
          <FeedbackButton target={{ type: "knowledge_page", id }} canModerate={canEdit} />
          {canEdit && (
            <>
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
            </>
          )}
        </div>
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
        <MarkdownRenderer content={page.body} refs={resolvedRefs} onNavigate={onNavigate} />
      </article>

      <RelatedPanel pageId={id} onNavigate={onNavigate} />
      <KnowledgeBacklinks urn={`mcp:knowledge_page:${id}`} onNavigate={onNavigate} />
      {canEdit && <RefPicker pageId={id} onNavigate={onNavigate} />}
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
      // Canonical normalization (trim, lowercase, de-dup) so the tag facet does
      // not fragment on case/duplicate variants.
      tags: parseTags(tags),
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
