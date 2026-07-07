import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import { useKnowledgePage, useCreateKnowledgePage, useUpdateKnowledgePage } from "@/api/portal/hooks";
import type { KnowledgePageInput, KnowledgePageDuplicateResponse } from "@/api/portal/types";
import { ApiError } from "@/api/portal/client";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { parseTags } from "@/lib/tags";

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
