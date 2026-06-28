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
import type { KnowledgePage, KnowledgePageInput, KnowledgePageDuplicateResponse } from "@/api/portal/types";
import { ApiError } from "@/api/portal/client";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { extractRefUrns } from "@/lib/entityRefs";
import { RelatedPanel } from "@/components/knowledge/RelatedPanel";
import { LineagePanel } from "@/components/knowledge/LineagePanel";
import { RefPicker } from "@/components/knowledge/RefPicker";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { useAuthStore } from "@/stores/auth";
import { parseTags } from "@/lib/tags";
import { FilterChip } from "@/components/FilterChip";
import { visibleFacetTags } from "./tagFacet";
import { useDebounced } from "@/lib/useDebounced";
import { MIN_SEARCH_LEN } from "@/api/portal/hooks";

type Mode = { view: "list" } | { view: "page"; id: string } | { view: "edit"; id: string } | { view: "create" };

/**
 * KnowledgePagesPage is the portal home for canonical business/domain knowledge
 * pages. Everyone can browse and read; create/edit/remove is shown only to
 * personas with apply_knowledge access (admins). It reuses the shared
 * MarkdownEditor and MarkdownRenderer.
 *
 * The page detail view is URL-addressable (#709): the open page is driven by the
 * /knowledge/pages/:id route via the openPageId prop, so detail is deep-linkable,
 * shareable, and supports browser back/forward. The hub keys this subtree by
 * path, so opening another page remounts with a fresh openPageId. Create and edit
 * are transient sub-states layered over the current route.
 */
export function KnowledgePagesPage({
  openPageId,
  onNavigate,
}: {
  // The knowledge page to open in detail, from the /knowledge/pages/:id route.
  // Undefined renders the page list (the /knowledge/pages route).
  openPageId?: string;
  // Navigate to an in-app path (page detail routing and entity-reference chips).
  onNavigate?: (path: string) => void;
} = {}) {
  const [mode, setMode] = useState<Mode>(
    openPageId ? { view: "page", id: openPageId } : { view: "list" },
  );

  // Open/leave page detail through real navigation when a navigator is present
  // (the hub always provides one) so the URL stays the source of truth; fall back
  // to in-component state when rendered standalone without a navigator.
  const openDetail = (id: string) =>
    onNavigate ? onNavigate(`/knowledge/pages/${id}`) : setMode({ view: "page", id });
  const backToList = () =>
    onNavigate ? onNavigate("/knowledge/pages") : setMode({ view: "list" });

  // Wiki-style back (#709): from page B reached by clicking through page A, "Back"
  // returns to A. AppShell records the path each navigation came from in
  // history.state.from, so we only step back through real browser history when the
  // previous entry was itself a knowledge page. Reaching this detail from anywhere
  // else (an asset viewer, a search result, a feedback surface, or a cold
  // deep-link) returns to the page list instead of ejecting out of Knowledge.
  const goBack = () => {
    const from = typeof window !== "undefined" ? window.history.state?.from : undefined;
    if (onNavigate && typeof from === "string" && from.startsWith("/knowledge/pages")) {
      window.history.back();
    } else {
      backToList();
    }
  };

  // Create/edit/remove gates on the apply_knowledge capability (a tool-access
  // gate, not an admin-role gate), or admin. This mirrors the REST handler's
  // userHasToolAccess (pkg/portal/knowledge_page_handler.go): the capability
  // grants non-admins, and admins are allowed too since apply_knowledge may be
  // unregistered on a deployment (#661).
  const canEdit = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );

  if (mode.view === "create") {
    return (
      <KnowledgePageForm
        key="create"
        // Cancel (onDone with no id) returns to the list in-component: the create
        // form never changed the URL (it is still /knowledge/pages), so navigating
        // there would be a no-op remount and leave the form on screen (#709).
        onDone={(id) => (id ? openDetail(id) : setMode({ view: "list" }))}
      />
    );
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
        onBack={goBack}
        onEdit={() => setMode({ view: "edit", id: mode.id })}
        onDeleted={backToList}
      />
    );
  }
  return <KnowledgePageList canEdit={canEdit} onOpen={openDetail} onCreate={() => setMode({ view: "create" })} />;
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
  // Whether the tag facet shows every tag or just the top TAG_FACET_LIMIT.
  const [tagsExpanded, setTagsExpanded] = useState(false);
  // Debounce the input and require a minimum length before searching, so the
  // content search issues one request after the user pauses rather than one per
  // keystroke. The hook enforces the same floor as a backstop.
  const debouncedQuery = useDebounced(query, 250);
  const trimmed = debouncedQuery.trim();
  const searching = trimmed.length >= MIN_SEARCH_LEN;
  // Searching hides the facet; collapse it so returning to browse starts from the
  // compact top-N view rather than a stale expansion from before the search.
  useEffect(() => {
    if (searching) setTagsExpanded(false);
  }, [searching]);
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

  // The capped facet chips, and how many tags that leaves hidden. The reveal
  // control only appears when something is actually hidden, so it is never a
  // dead button (e.g. when a selected over-limit tag is already pulled in).
  const visibleTags = visibleFacetTags(tagCounts, tag, tagsExpanded);
  const tagsHidden = tagCounts.length - visibleTags.length;

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

      {/* Tag browse (browse mode only). Cap the facet at TAG_FACET_LIMIT chips
          with a reveal for the rest, so a large tag set does not push the page
          list off-screen (#707). */}
      {!searching && tagCounts.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <FilterChip label="All" active={tag === ""} onClick={() => setTag("")} />
          {visibleTags.map(([t, c]) => (
            <FilterChip
              key={t}
              label={t}
              count={c}
              active={tag === t}
              onClick={() => setTag(tag === t ? "" : t)}
            />
          ))}
          {(tagsExpanded || tagsHidden > 0) && (
            <button
              type="button"
              onClick={() => setTagsExpanded((v) => !v)}
              className="rounded-full border border-dashed border-border px-2.5 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted"
            >
              {tagsExpanded ? "Show fewer" : `Show all (${tagCounts.length})`}
            </button>
          )}
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
          <ArrowLeft className="h-4 w-4" /> Back
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
        {page.tags && page.tags.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1.5">
            {page.tags.map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center rounded-full border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground"
              >
                {tag}
              </span>
            ))}
          </div>
        )}
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
      {canEdit && <LineagePanel pageId={id} />}
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

// isDuplicateResponse narrows an ApiError body to the create-time dedup 409 shape
// (#705), so the form can render candidates only when the payload really is one.
function isDuplicateResponse(body: unknown): body is KnowledgePageDuplicateResponse {
  return (
    typeof body === "object" &&
    body !== null &&
    (body as { duplicate_blocked?: unknown }).duplicate_blocked === true &&
    Array.isArray((body as { candidates?: unknown }).candidates)
  );
}

export function KnowledgePageForm({ id, onDone }: { id?: string; onDone: (id: string | null) => void }) {
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
  // dup holds the create-time near-duplicate candidates (#705) when the backend
  // blocks a create; the user then opens a candidate to consolidate onto it, or
  // forces a separate page.
  const [dup, setDup] = useState<KnowledgePageDuplicateResponse | null>(null);

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

  // buildInput assembles the create/update payload from the form fields, with
  // canonical tag normalization (trim, lowercase, de-dup) so the tag facet does not
  // fragment on case/duplicate variants. Used by both the create and update paths so
  // validation and shape stay in one place.
  const buildInput = (): KnowledgePageInput | null => {
    setError(null);
    if (!title.trim()) {
      setError("Title is required.");
      return null;
    }
    return { title: title.trim(), summary: summary.trim(), body, tags: parseTags(tags) };
  };

  const saveError = (e: unknown) => setError(e instanceof Error ? e.message : "Save failed.");

  // submitCreate runs the create mutation; forceNew bypasses the duplicate gate
  // (#705) after the user has chosen to create a separate page anyway. dup is cleared
  // up front so a non-409 failure on this attempt does not leave a stale banner.
  const submitCreate = (forceNew: boolean) => {
    setDup(null);
    const base = buildInput();
    if (!base) return;
    const input: KnowledgePageInput = forceNew ? { ...base, force_new: true } : base;
    create.mutate(input, {
      onSuccess: (p) => onDone(p.id),
      onError: (e: unknown) => {
        // A 409 duplicate_blocked is not a failure: surface the candidate pages so
        // the user can consolidate onto one, or force a separate page.
        if (e instanceof ApiError && e.status === 409 && isDuplicateResponse(e.body)) {
          setDup(e.body);
          return;
        }
        saveError(e);
      },
    });
  };

  const submit = () => {
    if (id) {
      const input = buildInput();
      if (!input) return;
      update.mutate({ id, input }, { onSuccess: (p) => onDone(p.id), onError: saveError });
      return;
    }
    submitCreate(false);
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

      {/* Create-time duplicate gate (#705): the backend blocked this create because
          its content closely matches existing pages. Offer to open a candidate (to
          consolidate onto it) or to create a separate page anyway. */}
      {dup && (
        <div className="space-y-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-3 text-sm">
          <p className="font-medium text-amber-700 dark:text-amber-400">Similar pages already exist</p>
          <p className="text-muted-foreground">
            Update existing knowledge instead of creating a duplicate. Open a page below to consolidate onto it, or create a separate page anyway.
          </p>
          <ul className="space-y-1">
            {dup.candidates.map((c) => (
              <li key={c.id}>
                <button
                  type="button"
                  onClick={() => onDone(c.id)}
                  className="text-left text-primary hover:underline"
                >
                  {c.title}
                  {c.slug ? <span className="text-muted-foreground"> ({c.slug})</span> : null}
                </button>
                <span className="ml-2 text-xs text-muted-foreground">{Math.round(c.score * 100)}% match</span>
              </li>
            ))}
          </ul>
          <div className="flex items-center gap-2 pt-1">
            <button
              type="button"
              onClick={() => submitCreate(true)}
              disabled={pending}
              className="rounded-md border border-border px-3 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50"
            >
              Create separate page anyway
            </button>
            <button
              type="button"
              onClick={() => setDup(null)}
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              Dismiss
            </button>
          </div>
        </div>
      )}

      {/* Persistent labels so each field stays identifiable once populated (the
          edit case), not just while the placeholder shows (#708). */}
      <div className="space-y-1">
        <label htmlFor="kp-title" className="text-xs font-medium text-muted-foreground">
          Title
        </label>
        <input
          id="kp-title"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Title"
          className="w-full rounded-md border border-border bg-background px-3 py-2 text-lg font-medium outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>
      <div className="space-y-1">
        <label htmlFor="kp-summary" className="text-xs font-medium text-muted-foreground">
          Summary <span className="font-normal opacity-70">(optional)</span>
        </label>
        {/* Multi-line so a two-sentence summary is fully readable without
            horizontal scroll (#708). */}
        <textarea
          id="kp-summary"
          value={summary}
          onChange={(e) => setSummary(e.target.value)}
          rows={3}
          placeholder="A sentence or two summarizing the page"
          className="w-full resize-y rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>
      <div className="space-y-1">
        <label htmlFor="kp-tags" className="text-xs font-medium text-muted-foreground">
          Tags <span className="font-normal opacity-70">(comma-separated, optional)</span>
        </label>
        <input
          id="kp-tags"
          value={tags}
          onChange={(e) => setTags(e.target.value)}
          placeholder="retail, pricing, seasonal"
          className="w-full rounded-md border border-border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-primary/40"
        />
      </div>
      <MarkdownEditor value={body} onChange={setBody} minHeight="420px" placeholder="Write the knowledge page in markdown..." />
    </div>
  );
}
