import { useQueries, useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  DirectoryUsersResponse,
  FeedbackTarget,
  PaginatedResponse,
  ThreadWithMeta,
} from "../types";
import { paginatedFetch, useOffsetInfiniteQuery, type InfiniteResult } from "./infinite";
import { THREAD_PAGE_SIZE } from "./threads";

// @-mention support for feedback comments (#627).

/** MentionCandidate is one person the composer may offer for a mention. */
export interface MentionCandidate {
  email: string;
  first_name?: string;
  last_name?: string;
  /** False for someone shared with who has never signed in. */
  confirmed: boolean;
}

/**
 * useMentionCandidates lists the people who can open the target, which is
 * exactly who a mention on it can reach. The server applies the same rule when
 * the comment is written, so the picker never offers someone whose mention
 * would be dropped.
 */
export function useMentionCandidates(target: FeedbackTarget, q: string, enabled = true) {
  const targetID = target.type === "standalone" ? "" : target.id;
  const params = new URLSearchParams({ target_type: target.type });
  if (targetID) params.set("target_id", targetID);
  if (q) params.set("q", q);
  return useQuery({
    queryKey: ["mention-candidates", target.type, targetID, q],
    queryFn: () =>
      apiFetch<{ candidates: MentionCandidate[] }>(`/mention-candidates?${params.toString()}`),
    enabled,
  });
}

/**
 * useMentionEligibility reports, per address written in a comment, whether that
 * person is in the target's audience. The composer needs it because a mention
 * can also be typed by hand: one outside the audience posts as ordinary text and
 * notifies nobody, and the author should learn that while writing rather than
 * from silence afterwards.
 *
 * Each address is checked as its own exact-match candidate query, so the answers
 * cache per person and a second mention of the same teammate costs nothing.
 */
export function useMentionEligibility(
  target: FeedbackTarget,
  emails: string[],
): Record<string, boolean | undefined> {
  const targetID = target.type === "standalone" ? "" : target.id;
  const results = useQueries({
    queries: emails.map((email) => ({
      queryKey: ["mention-eligibility", target.type, targetID, email],
      queryFn: () => {
        const params = new URLSearchParams({ target_type: target.type, q: email });
        if (targetID) params.set("target_id", targetID);
        return apiFetch<{ candidates: MentionCandidate[] }>(
          `/mention-candidates?${params.toString()}`,
        ).then((r) => r.candidates.some((c) => c.email.toLowerCase() === email.toLowerCase()));
      },
    })),
  });
  const out: Record<string, boolean | undefined> = {};
  emails.forEach((email, i) => {
    const result = results[i];
    out[email] = result?.isSuccess ? (result.data as boolean) : undefined;
  });
  return out;
}

/**
 * useDirectoryNames resolves display names for specific addresses, so a mention
 * chip reads as a name. It queries the directory per address rather than
 * pulling one page of it: a page is 50 rows ordered by name, so in any
 * organization larger than that most mentions would otherwise silently fall
 * back to a raw email address. Answers cache per person, so a thread that names
 * the same teammate repeatedly costs one request.
 */
export function useDirectoryNames(emails: string[]): Record<string, string> {
  const unique = Array.from(new Set(emails.map((e) => e.toLowerCase())));
  const results = useQueries({
    queries: unique.map((email) => ({
      queryKey: ["portal", "directory-name", email],
      queryFn: () =>
        apiFetch<DirectoryUsersResponse>(`/users?q=${encodeURIComponent(email)}`).then((r) => {
          const user = r.users.find((u) => u.email.toLowerCase() === email);
          return [user?.first_name, user?.last_name].filter(Boolean).join(" ");
        }),
    })),
  });
  const names: Record<string, string> = {};
  unique.forEach((email, i) => {
    const name = results[i]?.data;
    if (name) names[email] = name;
  });
  return names;
}

/** useMentionsWorklist counts the threads where a comment named the caller. */
export function useMentionsWorklist(enabled = true) {
  return useQuery({
    queryKey: ["worklist", "mentions"],
    queryFn: () => apiFetch<PaginatedResponse<ThreadWithMeta>>(`/worklist/mentions`),
    enabled,
  });
}

/** useInfiniteMentionsWorklist is the paged inbox list of those threads. */
export function useInfiniteMentionsWorklist(enabled = true): InfiniteResult<ThreadWithMeta> {
  return useOffsetInfiniteQuery<ThreadWithMeta>({
    queryKey: ["worklist", "mentions", "infinite"],
    pageSize: THREAD_PAGE_SIZE,
    keyOf: (t) => t.id,
    enabled,
    fetchPage: (offset, limit) => paginatedFetch<ThreadWithMeta>("/worklist/mentions", offset, limit),
  });
}
