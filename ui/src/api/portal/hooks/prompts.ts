import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { PaginatedResponse } from "../types";
import type { Prompt, PromptCollection, PromptUsage, PromptVersion } from "@/api/admin/types";

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

interface PortalPromptListResponse {
  personal: import("@/api/admin/types").Prompt[];
  available: import("@/api/admin/types").Prompt[];
}

export function useMyPrompts() {
  return useQuery({
    queryKey: ["portal", "prompts"],
    queryFn: () => apiFetch<PortalPromptListResponse>("/prompts"),
  });
}

// ScoredPrompt pairs a prompt with its relevance score, as returned by the
// ranked prompt search endpoint.
export interface ScoredPrompt {
  prompt: import("@/api/admin/types").Prompt;
  score: number;
}

// useSearchMyPrompts ranks approved prompts visible to the caller by relevance
// to query. Disabled (no request) until query is non-empty, so the list
// endpoint remains the default browse experience.
export function useSearchMyPrompts(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-my-prompts", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredPrompt>>(
        `/prompts/search?${sp.toString()}`,
      ),
  });
}

export function useCreateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      display_name?: string;
      description?: string;
      content: string;
      arguments?: { name: string; description: string; required: boolean }[];
      category?: string;
      tags?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>("/prompts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useUpdateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: {
      id: string;
      name?: string;
      display_name?: string;
      description?: string;
      content?: string;
      category?: string;
      tags?: string[];
      arguments?: { name: string; description: string; required: boolean }[];
      requested_scope?: string;
      requested_personas?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>(`/prompts/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useDeleteMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Prompt usage, versions, and collections (#1009 / #1010)
// ---------------------------------------------------------------------------

// usePromptUsage returns the audit-derived run count and last-run timestamp
// per prompt id for every prompt visible to the caller. Prompts never served
// are absent from the map.
export function usePromptUsage() {
  return useQuery({
    queryKey: ["portal", "prompt-usage"],
    queryFn: () =>
      apiFetch<Record<string, PromptUsage>>("/prompts/usage"),
  });
}

interface PromptVersionListResponse {
  data: PromptVersion[];
  total: number;
}

// usePromptVersions returns a prompt's version history, newest first. The
// server allows any caller who can view the prompt itself; a 403 (e.g. a
// prompt shared person-to-person) is surfaced as an error the UI treats as
// "history unavailable" rather than retried.
export function usePromptVersions(promptId: string | undefined) {
  return useQuery({
    queryKey: ["portal", "prompt-versions", promptId],
    enabled: !!promptId,
    retry: false,
    queryFn: () =>
      apiFetch<PromptVersionListResponse>(`/prompts/${promptId}/versions`),
  });
}

export function usePromptCollections() {
  return useQuery({
    queryKey: ["portal", "prompt-collections"],
    queryFn: () =>
      apiFetch<{ data: PromptCollection[]; total: number }>(
        "/prompt-collections",
      ),
  });
}

export function useCreatePromptCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; description?: string }) =>
      apiFetch<PromptCollection>("/prompt-collections", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompt-collections"] });
    },
  });
}

export function useUpdatePromptCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name: string; description?: string }) =>
      apiFetch<PromptCollection>(`/prompt-collections/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompt-collections"] });
    },
  });
}

export function useDeletePromptCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompt-collections/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      // Deleting a collection releases its member prompts, so both lists move.
      queryClient.invalidateQueries({ queryKey: ["portal", "prompt-collections"] });
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

// useAssignPromptCollection places a prompt in a collection (empty
// collection_id clears the assignment). Owners organize their own prompts;
// admins organize shared prompts.
export function useAssignPromptCollection() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, collectionId }: { id: string; collectionId: string }) =>
      apiFetch<Prompt>(`/prompts/${id}/collection`, {
        method: "PUT",
        body: JSON.stringify({ collection_id: collectionId }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
      queryClient.invalidateQueries({ queryKey: ["portal", "prompt-collections"] });
      // An admin can organize a prompt shared with them; that list caches the
      // prompt too.
      queryClient.invalidateQueries({ queryKey: ["shared-prompts"] });
    },
  });
}
