import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  PaginatedResponse,
  Thread,
  ThreadWithMeta,
  ThreadActivityItem,
  ThreadEvent,
  ThreadKind,
  ThreadStatus,
  ThreadTargetType,
  ThreadEventType,
  ThreadAnchor,
  ThreadChain,
  ThreadCounts,
  SignoffSummary,
} from "../types";

// --- Feedback threads (#601) ---

export interface ThreadListFilter {
  target_type?: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
  knowledge_page_id?: string;
  kind?: ThreadKind;
  status?: ThreadStatus;
  limit?: number;
  offset?: number;
}

function threadQuery(filter: ThreadListFilter): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(filter)) {
    if (v !== undefined && v !== null && v !== "") sp.set(k, String(v));
  }
  const qs = sp.toString();
  return qs ? `?${qs}` : "";
}

// A list filter is "scoped" once it targets a single object or the standalone
// channel; until then the query is disabled (the backend requires a scope).
function threadFilterScoped(filter: ThreadListFilter): boolean {
  return (
    filter.target_type === "standalone" ||
    !!filter.asset_id ||
    !!filter.collection_id ||
    !!filter.prompt_id ||
    !!filter.knowledge_page_id
  );
}

export function useThreads(filter: ThreadListFilter) {
  return useQuery({
    queryKey: ["threads", filter],
    queryFn: () =>
      apiFetch<PaginatedResponse<ThreadWithMeta>>(`/threads${threadQuery(filter)}`),
    enabled: threadFilterScoped(filter),
  });
}

export function useThread(id: string) {
  return useQuery({
    queryKey: ["thread", id],
    queryFn: () => apiFetch<Thread>(`/threads/${id}`),
    enabled: !!id,
  });
}

export function useThreadEvents(id: string) {
  return useQuery({
    queryKey: ["thread-events", id],
    queryFn: () =>
      apiFetch<{ data: ThreadEvent[] }>(`/threads/${id}/events`).then((r) => r.data),
    enabled: !!id,
  });
}

// Worklists / inbox (#603). Practitioner = open resolution-required threads on
// my artifacts; SME = threads awaiting my validation.
export function usePractitionerWorklist(enabled = true) {
  return useQuery({
    queryKey: ["worklist", "practitioner"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadWithMeta>>(`/worklist/practitioner`),
    enabled,
  });
}

export function useSMEWorklist(enabled = true) {
  return useQuery({
    queryKey: ["worklist", "sme"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadWithMeta>>(`/worklist/sme`),
    enabled,
  });
}

// useFeedbackActivity fetches the unified feed (#617): every feedback thread on
// an asset, collection, or prompt the caller can view, most recent first. With
// no push notifications, this is how a user discovers new feedback on their work.
export function useFeedbackActivity(enabled = true) {
  return useQuery({
    queryKey: ["feedback", "activity"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadActivityItem>>(`/feedback/activity`),
    enabled,
  });
}

// useSignoff fetches "signed off by N of M" for an asset or collection (#603).
export function useSignoff(targetType: "assets" | "collections", id: string, enabled = true) {
  return useQuery({
    queryKey: ["signoff", targetType, id],
    queryFn: () => apiFetch<SignoffSummary>(`/${targetType}/${id}/signoff`),
    enabled: !!id && enabled,
  });
}

// useRespondValidation lets the feedback author mark a thread validated/disputed
// (#603). Disputing re-opens the thread server-side.
export function useRespondValidation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      threadId,
      result,
      reason,
    }: {
      threadId: string;
      result: "validated" | "disputed";
      reason?: string;
    }) =>
      apiFetch<Thread>(`/threads/${threadId}/validation`, {
        method: "POST",
        body: JSON.stringify({ result, reason }),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

// useThreadChain fetches the resolved thread -> insight -> changeset chain
// (#602). Only enabled once a thread has been linked to an insight; an
// unlinked thread has nothing to show and we avoid the round-trip.
export function useThreadChain(id: string, hasInsight: boolean) {
  return useQuery({
    queryKey: ["thread-chain", id],
    queryFn: () => apiFetch<ThreadChain>(`/threads/${id}/chain`),
    enabled: !!id && hasInsight,
  });
}

export function useThreadCounts(
  targetType: "asset" | "collection" | "knowledge_page",
  ids: string[],
) {
  const sorted = [...ids].sort();
  return useQuery({
    queryKey: ["thread-counts", targetType, sorted],
    queryFn: () => {
      const sp = new URLSearchParams({ target_type: targetType, ids: sorted.join(",") });
      return apiFetch<ThreadCounts>(`/threads/counts?${sp.toString()}`);
    },
    enabled: sorted.length > 0,
  });
}

export interface CreateThreadInput {
  kind: ThreadKind;
  target_type: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
  knowledge_page_id?: string;
  anchor?: ThreadAnchor;
  target_version?: number;
  title?: string;
  requires_resolution?: boolean;
  body: string;
  rating?: number;
}

// invalidateThreadQueries refreshes every thread-related cache key after a
// mutation so lists, detail, timeline, and badges all reflect the change.
function invalidateThreadQueries(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["threads"] });
  void qc.invalidateQueries({ queryKey: ["thread"] });
  void qc.invalidateQueries({ queryKey: ["thread-events"] });
  void qc.invalidateQueries({ queryKey: ["thread-counts"] });
  // The activity feed and worklists (#617) are cross-artifact thread views, so a
  // create/reply/status change must refresh them too, not just the scoped lists.
  void qc.invalidateQueries({ queryKey: ["feedback"] });
  void qc.invalidateQueries({ queryKey: ["worklist"] });
}

export function useCreateThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateThreadInput) =>
      apiFetch<Thread>(`/threads`, {
        method: "POST",
        body: JSON.stringify(input),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

// Capturing a feedback thread as a reviewable insight (#662). The optional
// fields override the defaults (content derived from the thread, sink class
// business_knowledge). Requires apply_knowledge access server-side.
export interface CaptureThreadInsightInput {
  threadId: string;
  content?: string;
  category?: string;
  confidence?: string;
  sink_class?: string;
  entity_urns?: string[];
}

export interface CaptureThreadInsightResult {
  insight_id: string;
  status: string;
  linked: boolean;
}

export function useCaptureThreadInsight() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ threadId, ...body }: CaptureThreadInsightInput) =>
      apiFetch<CaptureThreadInsightResult>(`/threads/${threadId}/insight`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      invalidateThreadQueries(qc);
      // A new pending insight enters the review queue and its stats.
      void qc.invalidateQueries({ queryKey: ["insights"] });
      void qc.invalidateQueries({ queryKey: ["insight-stats"] });
    },
  });
}

export interface AppendThreadEventInput {
  threadId: string;
  event_type?: ThreadEventType;
  body?: string;
  rating?: number;
  parent_event_id?: string;
}

export function useAppendThreadEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ threadId, ...body }: AppendThreadEventInput) =>
      apiFetch<ThreadEvent>(`/threads/${threadId}/events`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

export interface UpdateThreadInput {
  id: string;
  status?: ThreadStatus;
  requires_resolution?: boolean;
  // validation_state is intentionally not settable via the generic update:
  // validation transitions go through useRespondValidation (#603).
}

export function useUpdateThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: UpdateThreadInput) =>
      apiFetch<Thread>(`/threads/${id}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}

export function useDeleteThread() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<{ status: string }>(`/threads/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateThreadQueries(qc),
  });
}
