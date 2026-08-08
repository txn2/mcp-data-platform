import { useState } from "react";
import { useKnowledgePage, useCreateKnowledgePage, useUpdateKnowledgePage } from "@/api/portal/hooks";
import type { KnowledgePageInput, KnowledgePageDuplicateResponse } from "@/api/portal/types";
import { ApiError } from "@/api/portal/client";
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

/** What the editor can render: the form, or why it cannot show one yet. */
export type EditorStatus = "ready" | "loading" | "failed";

/**
 * useKnowledgePageEditor holds a knowledge page while it is being written: the
 * field values, whether an existing page has been read into them, and what the
 * save attempt came back with. Create and edit differ only in which mutation
 * runs, so both live here and the form renders one shape.
 */
export function useKnowledgePageEditor(id: string | undefined, onDone: (id: string | null) => void) {
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
    if (!id) {
      submitCreate(false);
      return;
    }
    const input = buildInput();
    if (!input) return;
    update.mutate({ id, input }, { onSuccess: (p) => onDone(p.id), onError: saveError });
  };

  // In edit mode, the form is not rendered until the page has loaded, so the
  // user never sees (or saves) blank fields over real content.
  const status: EditorStatus = editorStatus(id, existing.isLoading, existing.isError);

  return {
    status,
    fields: { title, setTitle, summary, setSummary, body, setBody, tags, setTags },
    error,
    dup,
    dismissDup: () => setDup(null),
    pending,
    saveLabel: saveLabel(pending, id),
    submit,
    createAnyway: () => submitCreate(true),
  };
}

function editorStatus(id: string | undefined, isLoading: boolean, isError: boolean): EditorStatus {
  if (!id) return "ready";
  if (isLoading) return "loading";
  return isError ? "failed" : "ready";
}

function saveLabel(pending: boolean, id: string | undefined): string {
  if (pending) return "Saving...";
  return id ? "Save changes" : "Create page";
}
